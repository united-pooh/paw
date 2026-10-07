package bubble

import (
	"fmt"
	"strings"
)

type tikzNode struct {
	row, col int
	text     string
}
type tikzEdge struct {
	from, to int
	label    string
	swap     bool
}
type tikzDiagram struct {
	nodes      []tikzNode
	edges      []tikzEdge
	rows, cols int
}
type tikzArrow struct {
	dr, dc int
	label  string
	swap   bool
}

func renderTikZBlock(source, original string, width int) string {
	diagram, result := parseTikZ(source)
	if result.status != texSuccess {
		prefix := ""
		if result.status != texIncomplete {
			prefix = "[tikzcd：" + result.reason + "]\n"
		}
		return renderTeXLiteral(prefix+original, width)
	}
	if out, ok := layoutTikZ(diagram, width); ok {
		return out
	}
	return renderTeXLiteral(tikzRelationships(diagram), width)
}

func parseTikZ(source string) (tikzDiagram, texResult) {
	var d tikzDiagram
	fail := func(reason string) (tikzDiagram, texResult) {
		return d, texResult{status: texUnsupported, reason: reason}
	}
	if len(source) > maxTeXBytes {
		return fail("图形超过长度限制")
	}
	s := strings.TrimSpace(source)
	if strings.HasPrefix(s, `\begin{tikzcd}`) {
		body, end, ok := texEnvironmentBody(s, len(`\begin{tikzcd}`), "tikzcd")
		if !ok {
			return d, texResult{status: texIncomplete}
		}
		if strings.TrimSpace(s[end:]) != "" {
			return fail("图形环境后存在额外内容")
		}
		s = body
	}
	if strings.HasPrefix(strings.TrimSpace(s), "[") {
		return fail("暂不支持图形布局选项")
	}
	rows := texRows(s)
	for len(rows) > 1 && len(rows[len(rows)-1]) == 1 && strings.TrimSpace(rows[len(rows)-1][0]) == "" {
		rows = rows[:len(rows)-1]
	}
	d.rows = len(rows)
	for _, row := range rows {
		d.cols = maxInt(d.cols, len(row))
	}
	if d.rows > 8 || d.cols > 8 || d.rows*d.cols > 64 {
		return fail("图形最多支持 8×8 个格位")
	}
	pending := make(map[int][]tikzArrow)
	for r, row := range rows {
		for c := 0; c < d.cols; c++ {
			cell := ""
			if c < len(row) {
				cell = strings.TrimSpace(row[c])
			}
			node, arrows, reason := parseTikZCell(cell)
			if reason != "" {
				return fail(reason)
			}
			label := ""
			if strings.TrimSpace(node) != "" {
				result := renderTeXMath(node, true, 0)
				if result.status != texSuccess {
					return fail("节点：" + result.reason)
				}
				label = result.text
			}
			index := len(d.nodes)
			d.nodes = append(d.nodes, tikzNode{r, c, label})
			pending[index] = arrows
		}
	}
	for from := range d.nodes {
		for _, arrow := range pending[from] {
			node := d.nodes[from]
			r, c := node.row+arrow.dr, node.col+arrow.dc
			if r < 0 || r >= d.rows || c < 0 || c >= d.cols {
				return fail("箭头目标超出网格")
			}
			to := r*d.cols + c
			if from == to {
				return fail("暂不支持自环箭头")
			}
			d.edges = append(d.edges, tikzEdge{from: from, to: to, label: arrow.label, swap: arrow.swap})
			if len(d.edges) > 128 {
				return fail("图形超过 128 条边")
			}
		}
	}
	return d, texResult{status: texSuccess}
}

func parseTikZCell(s string) (string, []tikzArrow, string) {
	var node strings.Builder
	var arrows []tikzArrow
	depth := 0
	for i := 0; i < len(s); {
		if s[i] == '{' {
			depth++
		}
		if s[i] == '}' {
			depth--
			if depth < 0 {
				return "", nil, "节点括号不匹配"
			}
		}
		if depth > 32 {
			return "", nil, "节点嵌套过深"
		}
		if s[i] != '\\' {
			node.WriteByte(s[i])
			i++
			continue
		}
		name, end := texCommandAt(s, i)
		if (name != "ar" && name != "arrow") || depth != 0 {
			node.WriteString(s[i:end])
			i = end
			continue
		}
		for end < len(s) && s[end] == ' ' {
			end++
		}
		if end >= len(s) || s[end] != '[' {
			return "", nil, "箭头缺少方向选项"
		}
		options, j, ok := tikzOptions(s, end)
		if !ok {
			return "", nil, "箭头选项未闭合"
		}
		arrow, reason := parseTikZArrow(options)
		if reason != "" {
			return "", nil, reason
		}
		arrows = append(arrows, arrow)
		i = j
	}
	if depth != 0 {
		return "", nil, "节点括号未闭合"
	}
	return strings.TrimSpace(node.String()), arrows, ""
}

func tikzOptions(s string, start int) ([]string, int, bool) {
	var opts []string
	begin := start + 1
	depth := 0
	quote := false
	for i := begin; i < len(s); i++ {
		if s[i] == '\\' {
			_, i = texCommandAt(s, i)
			i--
			continue
		}
		if s[i] == '"' && depth == 0 {
			quote = !quote
		}
		if s[i] == '{' {
			depth++
		}
		if s[i] == '}' {
			depth--
		}
		if depth < 0 || depth > 32 {
			return nil, 0, false
		}
		if !quote && depth == 0 {
			if s[i] == ',' {
				opts = append(opts, strings.TrimSpace(s[begin:i]))
				begin = i + 1
			}
			if s[i] == ']' {
				return append(opts, strings.TrimSpace(s[begin:i])), i + 1, true
			}
		}
	}
	return nil, 0, false
}

func parseTikZArrow(options []string) (tikzArrow, string) {
	var a tikzArrow
	direction, labelSet := false, false
	for _, opt := range options {
		if opt == "swap" {
			a.swap = true
			continue
		}
		if strings.HasPrefix(opt, `"`) {
			if labelSet {
				return a, "暂不支持同一箭头多个标签"
			}
			labelSet = true
			tail := strings.TrimSpace(opt[1:])
			if strings.HasSuffix(tail, "'") {
				a.swap = true
				tail = strings.TrimSpace(strings.TrimSuffix(tail, "'"))
			}
			if !strings.HasSuffix(tail, `"`) {
				return a, "箭头标签未闭合"
			}
			label := strings.TrimSuffix(tail, `"`)
			result := renderTeXMath(label, true, 0)
			if result.status != texSuccess {
				return a, "标签：" + result.reason
			}
			a.label = result.text
			continue
		}
		if opt == "" || strings.Trim(opt, "rldu") != "" || direction {
			return a, "不支持箭头选项 " + opt
		}
		direction = true
		for _, ch := range opt {
			switch ch {
			case 'r':
				a.dc++
			case 'l':
				a.dc--
			case 'd':
				a.dr++
			case 'u':
				a.dr--
			}
		}
	}
	if !direction {
		return a, "箭头缺少方向"
	}
	return a, ""
}

func tikzRelationships(d tikzDiagram) string {
	var lines []string
	lines = append(lines, "[tikzcd：以关系文本显示]")
	label := func(id int) string {
		n := d.nodes[id]
		if n.text != "" {
			return n.text
		}
		return fmt.Sprintf("(行%d,列%d)", n.row+1, n.col+1)
	}
	// Coordinates disambiguate equal labels and preserve isolated nodes.
	for i, n := range d.nodes {
		if n.text != "" {
			lines = append(lines, fmt.Sprintf("(%d,%d) %s", n.row+1, n.col+1, label(i)))
		}
	}
	counts := map[string]int{}
	for _, n := range d.nodes {
		counts[n.text]++
	}
	endpoint := func(id int) string {
		n := d.nodes[id]
		s := label(id)
		if n.text != "" && counts[n.text] > 1 {
			s += fmt.Sprintf("(%d,%d)", n.row+1, n.col+1)
		}
		return s
	}
	for _, e := range d.edges {
		lines = append(lines, endpoint(e.from)+" —"+e.label+"→ "+endpoint(e.to))
	}
	return strings.Join(lines, "\n")
}

type tikzPoint struct{ x, y int }

func layoutTikZ(d tikzDiagram, width int) (string, bool) {
	colWidths := make([]int, d.cols)
	maxLabel := 0
	for _, n := range d.nodes {
		colWidths[n.col] = maxInt(colWidths[n.col], maxInt(1, terminalCellWidth(n.text)))
	}
	for _, e := range d.edges {
		maxLabel = maxInt(maxLabel, terminalCellWidth(e.label))
	}
	gap := maxInt(8, maxLabel+4)
	xs := make([]int, d.cols)
	total := 1
	for c, w := range colWidths {
		xs[c] = total + w/2
		total += w + gap
	}
	total -= gap
	total++
	height := maxInt(1, (d.rows-1)*6+3)
	if total > width || total*height > 20000 {
		return "", false
	}
	grid := make([][]string, height)
	for y := range grid {
		grid[y] = make([]string, total)
	}
	put := func(x, y int, s string) bool {
		if x < 0 || y < 0 || y >= height || x+terminalCellWidth(s) > total {
			return false
		}
		for col := x; col < x+terminalCellWidth(s); col++ {
			if grid[y][col] != "" {
				return false
			}
		}
		for s != "" {
			cluster, w := terminalFirstGraphemeCluster(s)
			if w == 0 {
				return false
			}
			grid[y][x] = cluster
			for k := 1; k < w; k++ {
				grid[y][x+k] = "\x00"
			}
			x += w
			s = s[len(cluster):]
		}
		return true
	}
	centers := make([]tikzPoint, len(d.nodes))
	left := make([]int, len(d.nodes))
	right := make([]int, len(d.nodes))
	for i, n := range d.nodes {
		p := tikzPoint{xs[n.col], n.row*6 + 1}
		centers[i] = p
		w := terminalCellWidth(n.text)
		left[i] = p.x - w/2
		right[i] = left[i] + maxInt(1, w) - 1
		if n.text != "" && !put(left[i], p.y, n.text) {
			return "", false
		}
	}
	for _, e := range d.edges {
		a, b := centers[e.from], centers[e.to]
		if d.nodes[e.from].text == "" || d.nodes[e.to].text == "" {
			return "", false
		}
		start, end := a, b
		if a.x != b.x {
			if b.x > a.x {
				start.x = right[e.from] + 1
				end.x = left[e.to] - 1
			} else {
				start.x = left[e.from] - 1
				end.x = right[e.to] + 1
			}
		} else if b.y > a.y {
			start.y++
			end.y--
		} else {
			start.y--
			end.y++
		}
		path := []tikzPoint{start}
		walk := func(target tikzPoint) {
			for path[len(path)-1] != target {
				p := path[len(path)-1]
				if p.x < target.x {
					p.x++
				} else if p.x > target.x {
					p.x--
				} else if p.y < target.y {
					p.y++
				} else {
					p.y--
				}
				path = append(path, p)
			}
		}
		if a.x != b.x && a.y != b.y {
			mid := (start.x + end.x) / 2
			walk(tikzPoint{mid, start.y})
			walk(tikzPoint{mid, end.y})
		}
		walk(end)
		for i, p := range path {
			before, after := a, b
			if i > 0 {
				before = path[i-1]
			}
			if i+1 < len(path) {
				after = path[i+1]
			}
			glyph := "─"
			if before.x == after.x {
				glyph = "│"
			} else if before.y != after.y {
				up, down, l, r := before.y < p.y || after.y < p.y, before.y > p.y || after.y > p.y, before.x < p.x || after.x < p.x, before.x > p.x || after.x > p.x
				switch {
				case up && l:
					glyph = "┘"
				case up && r:
					glyph = "└"
				case down && l:
					glyph = "┐"
				case down && r:
					glyph = "┌"
				}
			}
			if i == len(path)-1 {
				switch {
				case b.x > p.x:
					glyph = "→"
				case b.x < p.x:
					glyph = "←"
				case b.y > p.y:
					glyph = "↓"
				default:
					glyph = "↑"
				}
			}
			if !put(p.x, p.y, glyph) {
				return "", false
			}
		}
		if e.label != "" {
			x, y := (start.x+end.x)/2, (start.y+end.y)/2
			if a.x == b.x {
				side := 1
				if b.y < a.y {
					side = -1
				}
				if e.swap {
					side = -side
				}
				if side > 0 {
					x++
				} else {
					x -= terminalCellWidth(e.label)
				}
			} else {
				side := -1
				if b.x < a.x {
					side = 1
				}
				if e.swap {
					side = -side
				}
				y = start.y + side
				x = (start.x+end.x)/2 - terminalCellWidth(e.label)/2
			}
			if !put(x, y, e.label) {
				return "", false
			}
		}
	}
	var lines []string
	for _, row := range grid {
		var line strings.Builder
		for _, cell := range row {
			if cell == "" {
				line.WriteByte(' ')
			} else if cell != "\x00" {
				line.WriteString(cell)
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n"), true
}
