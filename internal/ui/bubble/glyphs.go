package bubble

// 图标注册表：一个语义 = 一个字形。
// 渲染代码一律从这里取字形，禁止在各渲染文件里散落字面量；
// glyphs_test.go 会断言注册表无重复、且渲染源码无裸字形。
const (
	glyphSuccess    = "✓" // 成功/完成
	glyphFailure    = "✗" // 失败（收编 ×、✕）
	glyphCanceled   = "■" // 已取消
	glyphWarning    = "!" // 警告
	glyphActive     = "●" // 活动/进行中圆点
	glyphFolded     = "▸" // 折叠（可展开）
	glyphExpanded   = "▾" // 已展开（可收起）
	glyphStatus     = "⚒" // 底部状态行图标
	glyphPrompt     = "›" // 输入提示符
	glyphNarration  = "::" // 助手叙述行前缀
	glyphThinking   = "*" // 思考折叠行前缀
	glyphChevronR   = "»" // 权限/模式提示前缀
)

// allGlyphs 供唯一性测试遍历；新增字形必须登记到这里。
var allGlyphs = []struct {
	semantic string
	glyph    string
}{
	{"成功", glyphSuccess},
	{"失败", glyphFailure},
	{"取消", glyphCanceled},
	{"警告", glyphWarning},
	{"活动", glyphActive},
	{"折叠", glyphFolded},
	{"展开", glyphExpanded},
	{"状态行", glyphStatus},
	{"输入提示", glyphPrompt},
	{"叙述", glyphNarration},
	{"思考行", glyphThinking},
	{"模式提示", glyphChevronR},
}
