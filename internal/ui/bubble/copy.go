package bubble

// 文案表：用户可见的中文文案集中在此，保证全仓文案体系统一。
// 英文残留（CTX / Ask anything / chat / Activity 等）逐步迁移到这里的常量；
// 键位名（ctrl+c 等）与命令词（/help）保持原样，不翻译。
const (
	// 输入区
	copyInputPlaceholder = "输入你的问题 …"
	copyInputHint        = "/help @文件 !shell"

	// 模式词
	copyModeChat = "聊天"
	copyModePlan = "计划"
	copyModeGoal = "目标"

	// 上下文条
	copyContextLabel = "上下文"

	// 状态行
	copyStatusThinking    = "思考中"
	copyStatusRunningTool = "执行 %s 中" // %s = 工具 chip 或名称
	copyStatusPermission  = "等待权限"
	copyStatusResuming    = "恢复中"
	copyStatusDone        = "完成"
	copyStatusInterrupt   = "ctrl+c 中断"

	// 工具状态词（出错统一改失败）
	copyToolStatusOK      = "完成"
	copyToolStatusRunning = "运行中"
	copyToolStatusError   = "失败"
	copyToolStatusUnknown = "未知"

	// 折叠行
	copyThinkingFold  = "思考了 %s"   // %s = 时长（"7 秒"）
	copyWorkedFold    = "工作了 %s"   // %s = 时长
	copyToolGroupFold = "%d 个工具"   // 组头前缀，后接文件清单
	copyGroupAllOK    = "全部完成"
	copyExpandHint    = "[[ctrl+O 展开]]"
	copyMoreLines     = "还有 %d 行"

	// 面板
	copyActivityTitle = "活动"
	copyTasksTitle    = "任务"
	copyTokenHistory  = "令牌历史"
	copyWorkspace     = "工作区"
	copyEmptyData     = "暂无数据"
	copySidebarHint   = "ctrl+G 活动面板"
)

// chip 标签（renderToolChip 使用）
const (
	copyChipRead   = "读取"
	copyChipEdit   = "编辑"
	copyChipWrite  = "写入"
	copyChipShell  = "Shell"
	copyChipSearch = "搜索"
	copyChipWeb    = "网页"
	copyChipTask   = "任务"
	copyChipTodo   = "待办"
	copyChipMemo   = "备忘"
	copyChipPlugin = "插件"
	copyChipTool   = "工具"
)
