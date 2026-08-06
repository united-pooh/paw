# 双击选中词翻译（Translate Selected Word on Double-Click）设计

## 需求

- `/setting` 新增「翻译选中词」开关（默认关闭）。
- 开关开启时，鼠标双击选中词 → 自动翻译该词。
- 英文词：展示 **音标 + 词性 + 中文释义**；中文词：展示英文翻译。
- 翻译通过**单次会话请求**完成（不进入 transcript、不占用上下文、不参与多轮对话）。
- 翻译指示写在**独立系统提示词**中；应用层**解析模型回复**（结构化 JSON），失败时回退纯文本。

## 现状（已具备的基础设施）

- 双击/三击选词：`selection.go` 的 `clickCount >= 2` → `startWordSelection`，选区高亮保留，拖拽释放才复制。
- 单次请求：`internal/model` 的 `Client.RunMessage(ctx, messages)` —— 非流式「输入→输出」，不写会话。
- 面板：`renderModalPanel`（`layout.go:293`），activity / 设置向导 / 会话选择器均复用。
- 设置：`internal/settings/settings.go` `UIConfig`；`/setting` 向导 `setting_wizard.go` 的 step 机制（选项列表 + confirm + save）。
- 消息构造：`message.Message{Role, Content}` 极简，bubble 包可直接构造。

## 方案选型

**方案 A（采纳）**：词典 JSON 模式 —— 翻译专用 system prompt 要求返回严格 JSON，
`json.Unmarshal` 解析出 word/phonetic/pos/translation/note 渲染面板；解析失败回退为
把模型回复原文当作译文显示。

方案 B（纯文本，无音标词性）与方案 C（工具调用，慢且 provider 兼容差）已否决。

## 变更清单

### 1. 设置项（internal/settings）

- `UIConfig` 新增字段：
  ```go
  TranslateOnDoubleClick bool `json:"translate_on_double_click"`
  ```
- `DefaultConfig` 默认 `false`；`Normalize` 无需特殊处理（bool 零值即关闭）。
- 迁移兼容：旧 settings.json 缺字段 → 零值 false，天然兼容。

### 2. /setting 向导（internal/ui/bubble/setting_wizard.go）

- 新增 step `settingWizardTranslate`（插在 context / run-mode 之后、confirm 之前）：
  - 选项两项：`on`（description: "double-click a word to translate it"）/ `off`。
  - `apply` 写 `cfg.UI.TranslateOnDoubleClick`。
- `newSettingWizard` 初始化该步的 selected 索引；`settingStepTitle` 增加标题
  （如 "Translate on double-click"）。
- confirm 摘要 `renderSettingsSummary` 增加一行 `ui.translate_on_double_click=...`。

### 3. 翻译请求（internal/ui/bubble/translate.go 新文件）

- **语言方向检测（应用层正则，不依赖模型自判）**：
  ```go
  var englishWordPattern = regexp.MustCompile(`^[A-Za-z]+(?:['-][A-Za-z]+)*$`) // don't / well-known
  var chineseWordPattern = regexp.MustCompile(`^[\p{Han}]+$`)                  // 纯汉字
  ```
  - 英文词 → system prompt 指示「英译中，给出 IPA 音标 + 词性 + 中文释义」。
  - 中文词 → system prompt 指示「中译英，给出英文翻译」。
  - 其他（混合/数字/符号）→ 通用指令「翻译该词，按内容判断语言方向」。
- 翻译专用系统提示词常量（按检测结果拼装方向指示，中文注释说明意图）：
  ```
  You are a dictionary translator. Respond ONLY with a JSON object:
  {"word": "...", "phonetic": "...", "pos": "...", "translation": "...", "note": "..."}
  - word: the input term as-is.
  - phonetic: IPA for English words (omit for Chinese input).
  - pos: part of speech like n./v./adj. (omit for Chinese input).
  - translation: Chinese meaning for English input; English meaning for Chinese input.
  - note: brief usage tip or example, or omit.
  ```
  方向指示按检测结果追加（如 "The input is English; translate it to Chinese."）。
- 执行方式：`model.NewClient(m.modelConfig.CurrentModelConfig())` +
  `client.RunMessage(ctx, messages)`，包装为 `tea.Cmd`，结果通过新消息
  `translateResultMsg{seq, word, text, err}` 回到 Update。
- 请求序号 `translateSeq`：每次双击发起翻译时自增；返回结果若 seq 落后于当前
  值则丢弃（防止旧请求覆盖新面板）。
- 可测试性：`var runTranslateRequest = func(ctx, cfg, word) (string, error)` 包级
  变量（与 `writeClipboard` 同模式），测试可替换为 fake。

### 4. 触发（internal/ui/bubble/selection.go）

- press 分支 `clickCount >= 2` 调 `startWordSelection` 后：
  ```go
  if m.currentSettings().UI.TranslateOnDoubleClick {
      if word := m.selectedTranscriptText(); word != "" {
          m.translatePanel = newTranslatePanel(word)   // loading 态
          m.translateSeq++
          return m, true, startTranslateCmd(m.translateSeq, word)
      }
  }
  ```
- 面板打开期间再次双击新词 → 直接切换（seq 递增丢弃旧响应）。

### 5. 面板（internal/ui/bubble/translate.go + layout.go）

- `appModel` 新增字段 `translatePanel *translatePanel`（word、state、phonetic、
  pos、translation、note、err）。
- `layout.go` 的 modal 渲染优先级：现有各 modal 之后追加翻译面板
  （`renderTranslatePanel` 复用 `renderModalPanel`）。
- 面板内容三态：
  - loading：`翻译中…`（复用 `generatingStatusStyle` 类样式）。
  - done：原词（加粗）+ 音标 + 词性 + 释义 + note。
  - error：`labelErrorStyle` 显示错误。
- 关闭：esc（`handleSettingWizardKey` 之外新增按键分支，或并入现有 esc 处理链）；
  面板打开时 esc 优先关面板。
- 面板不拦截其他键（输入框仍可打字？——保持与 activity 面板一致：esc 关闭）。

### 6. 测试

- `internal/settings/settings_test.go`：字段序列化/反序列化、默认 false。
- `setting_wizard_test.go` 区域：新步骤导航、on/off 选择写入 draft、confirm 摘要。
- `translate_test.go`（新）：
  - 双击触发：fake runner 断言请求 messages 含翻译 system prompt 与选中词。
  - JSON 解析：音标/词性/释义渲染。
  - 兜底：非法 JSON → 原文显示。
  - 过期响应丢弃：seq 落后时面板不被覆盖。
  - 错误态：RunMessage 返回 error → 面板显示错误。
  - esc 关闭面板；关闭后选区保留。

## 交互细节

- 开关关闭时行为与现状完全一致（双击仅选词）。
- 翻译请求失败不影响会话与输入；面板错误可重试？——首版不做重试，esc 后重新双击。
- 请求期间面板显示 loading；超时由 client 的 `Config.Timeout` 控制。

## 不做（首版范围外）

- 音标朗读、发音按钮。
- 翻译历史 / 生词本。
- 例句展开与多义项折叠。
- 长文本（> 某长度）双击翻译限制——由词选区天然约束（双击只选一个词）。
