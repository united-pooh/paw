# 流式输出（SSE Streaming）实现教程

本文件对应 `stream.go`，逐层讲解「用 Go 消费 OpenAI 兼容流式接口」的完整实现思路。

---

## 目录

1. [什么是 SSE 流式输出](#1-什么是-sse-流式输出)
2. [整体数据流](#2-整体数据流)
3. [StreamEvent：统一事件模型](#3-streamevent统一事件模型)
4. [StreamMessage：发起请求并返回 channel](#4-streammessage发起请求并返回-channel)
5. [consumeStream：后台 goroutine 解析 SSE](#5-consumestream后台-goroutine-解析-sse)
6. [emitStreamEvent：ctx 感知的安全发送](#6-emitstreameventctx-感知的安全发送)
7. [调用方如何使用](#7-调用方如何使用)
8. [深入：bufio 是什么，怎么用](#8-深入bufio-是什么怎么用)
9. [常见问题与设计决策](#9-常见问题与设计决策)

---

## 1. 什么是 SSE 流式输出

**Server-Sent Events（SSE）** 是一种 HTTP 长连接技术：服务端在单次响应中持续向客户端推送文本行，每一行以 `data:` 开头，以空行作为分隔符，最终用特殊标记 `[DONE]` 表示结束。

```
HTTP/1.1 200 OK
Content-Type: text/event-stream

data: {"choices":[{"delta":{"content":"你"},"finish_reason":null}]}

data: {"choices":[{"delta":{"content":"好"},"finish_reason":null}]}

data: {"choices":[{"delta":{"content":"！"},"finish_reason":"stop"}]}

data: [DONE]
```

OpenAI 及兼容接口（DeepSeek 等）均采用此格式实现"打字机"效果。

---

## 2. 整体数据流

```
调用方
  │
  │  调用 StreamMessage(ctx, messages)
  ▼
StreamMessage
  ├─ 构造 HTTP POST 请求（stream=true）
  ├─ 发送请求，检查状态码
  ├─ 创建 events channel
  ├─ 启动后台 goroutine: go consumeStream(...)
  └─ 立即返回 (<-chan StreamEvent, nil)
        │
        │  调用方 range events
        ▼
  consumeStream（后台 goroutine）
  ├─ bufio.Scanner 逐行读取 resp.Body
  ├─ 过滤空行、非 data: 行
  ├─ 解析 JSON payload
  ├─ 遇到 Delta  → emitStreamEvent(Delta)
  ├─ 遇到 [DONE] → emitStreamEvent(Done)
  └─ 遇到 Error  → emitStreamEvent(Err)
        │
        └─ defer close(events)  ← 保证 range 一定结束
```

关键设计原则：
- `StreamMessage` 是同步的（HTTP 握手阶段），只要 TCP 连接成功就立刻返回 channel。
- 后续的字节读取、解析、转发全部在后台 goroutine 中完成，不阻塞调用方。

---

## 3. StreamEvent：统一事件模型

```go
// stream.go:20-24
type StreamEvent struct {
    Delta string  // 当前增量文本片段（非空时有效）
    Done  bool    // 为 true 时表示整个流已结束
    Err   error   // 非 nil 时表示发生了错误
}
```

三个字段互斥，每次只有一种状态被置位：

| 情况 | Delta | Done | Err |
|------|-------|------|-----|
| 收到一段文字 | `"你好"` | `false` | `nil` |
| 流正常结束 | `""` | `true` | `nil` |
| 发生错误 | `""` | `false` | `<error>` |

这种"单一职责事件"设计让调用方的 switch 逻辑极为清晰，避免了多字段同时非零带来的歧义。

---

## 4. StreamMessage：发起请求并返回 channel

```go
// stream.go:46-102
func (c *Client) StreamMessage(ctx context.Context, messages []message.Message) (<-chan StreamEvent, error) {
```

### 4.1 构造请求体

```go
reqBody := ChatCompletionsRequest{
    Model:    c.cfg.Model,
    Messages: messages,
    Stream:   true,   // ← 关键：告知服务端启用 SSE
}
```

`Stream: true` 在序列化为 JSON 后会产生 `"stream":true`，服务端据此切换为流式响应模式。

### 4.2 透传 Context

```go
req, err := http.NewRequestWithContext(ctx, http.MethodPost, ...)
```

将上层传入的 `ctx` 绑定到 HTTP 请求，使得：
- 超时自动触发（`Config.Timeout` 已设置在 `http.Client` 上）
- 调用方主动取消时，底层 TCP 连接会被中断

### 4.3 非 2xx 同步报错

```go
if resp.StatusCode < 200 || resp.StatusCode >= 300 {
    // 读取错误体，同步返回 error
}
```

SSE 连接建立阶段的错误（如 401 鉴权失败、429 限流）在这里被捕获，调用方无需启动 range 就能知道失败原因。

### 4.4 异步化：创建 channel + 启动 goroutine

```go
events := make(chan StreamEvent)      // 无缓冲：背压天然存在
go c.consumeStream(ctx, resp, events) // 后台消费
return events, nil                    // 调用方立即拿到 channel
```

无缓冲 channel 保证：消费方处理速度慢时，goroutine 会自然阻塞，不会在内存中积压大量事件。

---

## 5. consumeStream：后台 goroutine 解析 SSE

```go
// stream.go:105-184
func (c *Client) consumeStream(ctx context.Context, resp *http.Response, events chan<- StreamEvent) {
```

### 5.1 资源释放保证

```go
defer close(events)
defer resp.Body.Close()
```

无论函数以何种路径退出（正常结束、`[DONE]`、错误、ctx 取消），这两个 defer 都会执行：
- `close(events)` 让 `range events` 正常退出，避免调用方永久阻塞
- `resp.Body.Close()` 释放底层 TCP 连接

### 5.2 逐行扫描 SSE

```go
scanner := bufio.NewScanner(resp.Body)
scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 扩大到 1MB
```

SSE 是文本行协议，`bufio.Scanner` 是最自然的工具。
默认缓冲仅 64KB，模型返回含 markdown 的长段落时容易触发 `bufio.ErrTooLong`，因此扩大到 1MB。

### 5.3 行过滤

```go
line := strings.TrimSpace(scanner.Text())
if line == "" { continue }               // SSE 分隔空行
if !strings.HasPrefix(line, "data:") { continue } // 忽略 event:/id:/comment: 行
```

SSE 规范允许多种行类型，目前只关心 `data:` 行，其他行跳过不处理。

### 5.4 解析 payload

```go
payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
if payload == "[DONE]" { ... return }   // 终止信号
var chunk chatCompletionsStreamResponse
json.Unmarshal([]byte(payload), &chunk) // 解析增量 JSON
```

`chatCompletionsStreamResponse` 是刻意精简的私有结构体，只包含本层需要的字段，防止上层依赖供应商完整响应格式：

```go
// stream.go:28-39
type chatCompletionsStreamResponse struct {
    Choices []struct {
        Delta        struct{ Content string }
        FinishReason *string
    }
    Error *struct{ Message, Type string }
}
```

### 5.5 两种结束信号

OpenAI 兼容接口实践中存在两种流结束信号，代码都做了处理：

| 信号 | 形式 | 处理位置 |
|------|------|---------|
| SSE 协议层结束 | `data: [DONE]` | `payload == "[DONE]"` 分支 |
| 应用层结束 | `finish_reason != ""` | `finishReason` 非空分支 |

两个分支都会发送 `StreamEvent{Done: true}` 并 return，保证上层只收到一次 Done。

### 5.6 Scanner 读取错误

```go
if err := scanner.Err(); err != nil {
    _ = emitStreamEvent(ctx, events, StreamEvent{Err: ...})
}
```

`scanner.Err()` 只在非 EOF 错误（如网络中断）时非 nil，正常 EOF 返回 nil。

---

## 6. emitStreamEvent：ctx 感知的安全发送

```go
// stream.go:188-195
func emitStreamEvent(ctx context.Context, events chan<- StreamEvent, ev StreamEvent) bool {
    select {
    case events <- ev:
        return true
    case <-ctx.Done():
        return false
    }
}
```

**为什么需要这个函数？**

直接写 `events <- ev` 存在死锁风险：如果调用方在 range 中途取消了 ctx（或 panic 退出），channel 没有接收方，goroutine 会永远阻塞在发送上，`defer resp.Body.Close()` 永远不会执行，导致连接泄漏。

`select` 同时监听 `ctx.Done()`，一旦 ctx 被取消，立即放弃发送并返回 `false`，函数调用方据此决定是否继续或直接 return。

---

## 7. 调用方如何使用

典型消费模式：

```go
events, err := client.StreamMessage(ctx, messages)
if err != nil {
    // 连接建立阶段失败，直接处理
    return err
}

var fullText strings.Builder
for ev := range events {           // close(events) 后自动退出
    if ev.Err != nil {
        return ev.Err
    }
    if ev.Done {
        break                      // 也可以不 break，range 会自然结束
    }
    fmt.Print(ev.Delta)            // 实时打印增量文字
    fullText.WriteString(ev.Delta)
}
```

注意：
- `range events` 在 `close(events)` 之后会自动退出，无需手动判断 channel 关闭
- 收到 `Done` 后 channel 很快就会被关闭，`break` 与否均可
- 发生错误后 goroutine 会立即关闭 channel，range 也会退出

---

## 8. 深入：bufio 是什么，怎么用

### 8.1 bufio 的定位

`bufio` 是 Go 标准库中的**带缓冲 I/O 包**（buffered I/O）。它不直接读写底层设备，而是在已有的 `io.Reader` / `io.Writer` 上套一层内存缓冲区，以减少系统调用次数，并提供按行、按分隔符读取的能力。

```
网络/文件 (io.Reader)
       │
       ▼
  bufio.Reader / bufio.Scanner   ← 内存缓冲区 + 高级读取 API
       │
       ▼
    你的代码
```

**为什么要缓冲？**
每次调用 `Read()` 都可能触发一次系统调用（syscall）。如果每次只读 1 个字节，成本极高。`bufio` 会一次性从底层读取一大块数据放入缓冲区，后续的小读操作直接从缓冲区取，大幅降低 syscall 频率。

---

### 8.2 bufio 的三个核心类型

| 类型 | 用途 |
|------|------|
| `bufio.Reader` | 带缓冲的读取器，提供 `ReadLine`、`ReadString`、`ReadBytes` 等 |
| `bufio.Writer` | 带缓冲的写入器，提供 `WriteString`、`Flush` 等 |
| `bufio.Scanner` | 专门用于「按 token 扫描」，默认按行，最常用 |

在 `stream.go` 中使用的是 **`bufio.Scanner`**，它是读取 SSE 文本行的最自然工具。

---

### 8.3 bufio.Scanner 基础用法

```go
package main

import (
    "bufio"
    "fmt"
    "strings"
)

func main() {
    input := "第一行\n第二行\n第三行"
    reader := strings.NewReader(input)

    scanner := bufio.NewScanner(reader)
    for scanner.Scan() {         // Scan() 读取下一个 token，读完返回 false
        fmt.Println(scanner.Text()) // Text() 返回当前 token 的字符串
    }
    if err := scanner.Err(); err != nil {
        fmt.Println("读取出错:", err)
    }
}
// 输出：
// 第一行
// 第二行
// 第三行
```

**核心 API 一览：**

```go
scanner := bufio.NewScanner(r io.Reader) // 创建，默认按行分割
scanner.Scan()   bool    // 读取下一个 token；返回 false 表示结束或出错
scanner.Text()   string  // 当前 token 内容（string 形式，不含换行符）
scanner.Bytes()  []byte  // 当前 token 内容（[]byte 形式，底层共享，需复制）
scanner.Err()    error   // 返回非 EOF 错误；正常读完返回 nil
```

> `Scan()` 遇到 EOF 时返回 `false`，`Err()` 也返回 `nil`。
> 只有真正出错（网络断开、缓冲溢出等）时，`Err()` 才非 nil。

---

### 8.4 在 stream.go 中的具体用法

```go
// stream.go:113-115
scanner := bufio.NewScanner(resp.Body)
scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
```

**第一行**：以 HTTP 响应体 `resp.Body`（实现了 `io.Reader`）构建 Scanner。

**第二行**：`Buffer(buf []byte, max int)` 自定义缓冲区——
- 第一个参数 `make([]byte, 0, 64*1024)`：初始容量 64KB 的空 slice，作为缓冲区起点
- 第二个参数 `1024*1024`：单个 token（一行）允许的最大字节数，即 1MB

不调用 `Buffer` 时，默认最大行长仅为 `bufio.MaxScanTokenSize`（64KB）。模型输出长段落时容易超限，触发 `bufio.ErrTooLong` 错误，所以显式扩大到 1MB。

```go
// stream.go:117-174
for scanner.Scan() {
    line := strings.TrimSpace(scanner.Text()) // 取出当前行，去掉首尾空白
    if line == "" { continue }                // 跳过 SSE 空行分隔符
    if !strings.HasPrefix(line, "data:") { continue }
    // ... 解析 payload ...
}
if err := scanner.Err(); err != nil {
    // 网络中断、缓冲溢出等非 EOF 错误
}
```

---

### 8.5 自定义分割函数（SplitFunc）

Scanner 默认按换行符分割（`bufio.ScanLines`），也可以换成其他策略：

```go
// 按单词分割
scanner.Split(bufio.ScanWords)

// 按字节分割（每次读 1 字节）
scanner.Split(bufio.ScanBytes)

// 按 Unicode 字符分割
scanner.Split(bufio.ScanRunes)

// 自定义：按 "\r\n\r\n" 分割（HTTP 头部场景）
scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
    if i := bytes.Index(data, []byte("\r\n\r\n")); i >= 0 {
        return i + 4, data[:i], nil
    }
    return 0, nil, nil // 数据不够，等待更多
})
```

SSE 是行协议，默认的 `ScanLines` 完全满足需求，所以 `stream.go` 没有自定义 Split。

---

### 8.6 bufio.Reader vs bufio.Scanner 如何选？

| 场景 | 推荐 |
|------|------|
| 按行/按分隔符迭代，不需要精细控制 | `bufio.Scanner` ✅（stream.go 的选择） |
| 需要 `UnreadByte`、`Peek`、`ReadByte` 等底层控制 | `bufio.Reader` |
| 读取二进制数据，分隔符不固定 | `bufio.Reader` |
| 简单的「逐行处理文本文件/流」 | `bufio.Scanner` ✅ |

---

## 9. 常见问题与设计决策

### Q: 为什么用无缓冲 channel 而不是带缓冲？

无缓冲 channel 提供天然背压：如果调用方（UI 渲染）来不及消费，goroutine 会等待，网络读取也随之暂缓，避免在内存中堆积大量 Delta。如果改用带缓冲 channel，在调用方卡住时可能积压大量已解析内容。

### Q: consumeStream 为什么是 Client 的方法而不是包级函数？

`Client` 持有 `cfg`（目前 consumeStream 未直接用到，但方便后续扩展日志、指标上报等），同时让 goroutine 的归属关系更清晰。

### Q: 为什么同步错误（非 2xx）在 StreamMessage 里返回，而不是通过 channel？

分层清晰：StreamMessage 的 error 返回值表示"连接无法建立"；channel 中的 Err 事件表示"连接已建立但流中出现了问题"。调用方可以用两段不同的错误处理逻辑分别应对。

### Q: [DONE] 和 finish_reason 都处理会不会发两次 Done？

不会。两个分支都会在发送 Done 后立即 `return`，所以只有先触发的那个会执行。实践中 `[DONE]` 通常紧跟在带 `finish_reason` 的 chunk 之后，但顺序不绝对，两个分支都处理才能保证健壮性。
