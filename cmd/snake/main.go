package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	gridWidth  = 20
	gridHeight = 15
	tickInterval = 150 * time.Millisecond
)

type Point struct {
	X, Y int
}

type Direction int

const (
	DirUp Direction = iota
	DirDown
	DirLeft
	DirRight
)

type GameState int

const (
	StatePlaying GameState = iota
	StateGameOver
)

type model struct {
	snake    []Point
	food     Point
	dir      Direction
	nextDir  Direction
	state    GameState
	score    int
	width    int
	height   int
}

func (m model) Init() tea.Cmd {
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

type tickMsg struct{}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "w":
			if m.dir != DirDown {
				m.nextDir = DirUp
			}
		case "down", "s":
			if m.dir != DirUp {
				m.nextDir = DirDown
			}
		case "left", "a":
			if m.dir != DirRight {
				m.nextDir = DirLeft
			}
		case "right", "d":
			if m.dir != DirLeft {
				m.nextDir = DirRight
			}
		case "r", "enter":
			if m.state == StateGameOver {
				m = newModel(m.width, m.height)
				return m, tickCmd()
			}
		}

	case tickMsg:
		if m.state == StatePlaying {
			m.dir = m.nextDir
			head := m.snake[0]
			var newHead Point
			switch m.dir {
			case DirUp:
				newHead = Point{head.X, head.Y - 1}
			case DirDown:
				newHead = Point{head.X, head.Y + 1}
			case DirLeft:
				newHead = Point{head.X - 1, head.Y}
			case DirRight:
				newHead = Point{head.X + 1, head.Y}
			}

			// Wall collision
			if newHead.X < 0 || newHead.X >= m.width || newHead.Y < 0 || newHead.Y >= m.height {
				m.state = StateGameOver
				return m, nil
			}

			// Self collision
			for _, p := range m.snake {
				if p == newHead {
					m.state = StateGameOver
					return m, nil
				}
			}

			// Move snake
			m.snake = append([]Point{newHead}, m.snake...)

			// Check food
			if newHead == m.food {
				m.score++
				m.food = randomFood(m.width, m.height, m.snake)
			} else {
				m.snake = m.snake[:len(m.snake)-1]
			}

			return m, tickCmd()
		}
	}

	return m, nil
}

func randomFood(w, h int, snake []Point) Point {
	for {
		p := Point{rand.Intn(w), rand.Intn(h)}
		collision := false
		for _, s := range snake {
			if s == p {
				collision = true
				break
			}
		}
		if !collision {
			return p
		}
	}
}

var (
	snakeStyle  = lipgloss.NewStyle().Background(lipgloss.Color("#00ff00"))
	foodStyle   = lipgloss.NewStyle().Background(lipgloss.Color("#ff0000"))
	emptyStyle  = lipgloss.NewStyle().Background(lipgloss.Color("#1a1a2e"))
	headStyle   = lipgloss.NewStyle().Background(lipgloss.Color("#00cc00"))
	wallStyle   = lipgloss.NewStyle().Background(lipgloss.Color("#16213e"))
	scoreStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00")).Bold(true)
	titleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00")).Bold(true).MarginBottom(1)
	gameOverStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff4444")).Bold(true)
)

func (m model) View() string {
	var s string

	s += titleStyle.Render("🐍 贪吃蛇 Snake Game") + "\n"
	s += scoreStyle.Render(fmt.Sprintf("Score: %d", m.score)) + "\n\n"

	// Draw top border
	for x := 0; x < m.width+2; x++ {
		s += wallStyle.Render("  ")
	}
	s += "\n"

	for y := 0; y < m.height; y++ {
		// Left border
		s += wallStyle.Render("  ")

		for x := 0; x < m.width; x++ {
			p := Point{x, y}
			isSnake := false
			isHead := false
			for i, sp := range m.snake {
				if sp == p {
					isSnake = true
					if i == 0 {
						isHead = true
					}
					break
				}
			}
			switch {
			case isHead:
				s += headStyle.Render("  ")
			case isSnake:
				s += snakeStyle.Render("  ")
			case p == m.food:
				s += foodStyle.Render("  ")
			default:
				s += emptyStyle.Render("  ")
			}
		}

		// Right border
		s += wallStyle.Render("  ")
		s += "\n"
	}

	// Draw bottom border
	for x := 0; x < m.width+2; x++ {
		s += wallStyle.Render("  ")
	}
	s += "\n\n"

	s += lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render(
		"Arrow keys / WASD: Move  |  R/Enter: Restart  |  Q: Quit",
	) + "\n"

	if m.state == StateGameOver {
		s += "\n" + gameOverStyle.Render(fmt.Sprintf("💀 Game Over! Final Score: %d", m.score)) + "\n"
		s += lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render("Press R or Enter to restart") + "\n"
	}

	return s
}

func newModel(w, h int) model {
	centerX := w / 2
	centerY := h / 2
	snake := []Point{
		{centerX, centerY},
		{centerX - 1, centerY},
		{centerX - 2, centerY},
	}
	food := randomFood(w, h, snake)
	return model{
		snake:  snake,
		food:   food,
		dir:    DirRight,
		nextDir: DirRight,
		state:  StatePlaying,
		score:  0,
		width:  w,
		height: h,
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())

	m := newModel(gridWidth, gridHeight)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Println("Error running game:", err)
		os.Exit(1)
	}
}
