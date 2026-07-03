.PHONY: dev rebuild test build clean

## 一键启动（构建 + 运行 Agent + 打开 Paw）
dev:
	@./scripts/dev.sh

## 强制重新构建 Paw.app 后启动
rebuild:
	@./scripts/dev.sh --rebuild

## 只构建 Go agent
build:
	@go build ./cmd/agent/...

## 运行所有测试
test:
	@go test ./...

## 只启动 Agent 服务（不打开 Paw）
serve:
	@AGENT_WS_PORT=$${AGENT_WS_PORT:-8765} go run ./cmd/agent/...

## 清理构建产物
clean:
	@rm -f /tmp/codex-agent
	@rm -rf /Users/united_pooh/PyProjects/Paw/build
	@echo "cleaned"
