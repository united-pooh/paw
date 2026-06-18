package streamma

import (
	"context"
	"errors"
	"fmt"
	"gocode/internal/model"
	"strings"
)

type ParserConfig struct {
	RunID        string
	AgentID      string
	Boundary     string
	MaxStepBytes int
	StartIndex   int
	InputEvents  []string
}

type ParserFatalError struct {
	AgentID string
	Reason  string
}

func (e *ParserFatalError) Error() string {
	if e == nil {
		return ""
	}
	if e.AgentID == "" {
		return "parser fatal: " + e.Reason
	}
	return fmt.Sprintf("parser fatal for %s: %s", e.AgentID, e.Reason)
}

func IsParserFatal(err error) bool {
	var fatal *ParserFatalError
	return errors.As(err, &fatal)
}

func ParseStream(ctx context.Context, events <-chan model.StreamEvent, config ParserConfig) ([]StepPacket, error) {
	return parseStream(ctx, events, config, nil)
}

func StreamSteps(ctx context.Context, events <-chan model.StreamEvent, config ParserConfig, emit func(StepPacket) error) error {
	_, err := parseStream(ctx, events, config, emit)
	return err
}

func parseStream(ctx context.Context, events <-chan model.StreamEvent, config ParserConfig, emit func(StepPacket) error) ([]StepPacket, error) {
	parser := newStepParser(config)
	parser.emit = emit
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case event, ok := <-events:
			if !ok {
				return parser.finish()
			}
			if event.Err != nil {
				return nil, event.Err
			}
			if event.Usage != nil {
				parser.recordUsage(*event.Usage)
			}
			if event.Delta != "" {
				if err := parser.consume(event.Delta); err != nil {
					return nil, err
				}
			}
			if event.Done {
				return parser.finish()
			}
		}
	}
}

type stepParser struct {
	config      ParserConfig
	boundary    string
	emit        func(StepPacket) error
	pending     string
	current     strings.Builder
	inCodeBlock bool
	nextIndex   int
	usage       StepUsage
	steps       []StepPacket
}

func newStepParser(config ParserConfig) *stepParser {
	boundary := strings.TrimSpace(config.Boundary)
	if boundary == "" {
		boundary = DefaultBoundary
	}
	start := config.StartIndex
	if start < 1 {
		start = 1
	}
	return &stepParser{
		config:    config,
		boundary:  boundary,
		nextIndex: start,
	}
}

func (p *stepParser) recordUsage(usage model.Usage) {
	p.usage = StepUsage{
		InputTokens:  usage.PromptTokenCount(),
		CachedTokens: usage.CacheHitTokens(),
		OutputTokens: usage.CompletionTokenCount(),
	}
}

func (p *stepParser) consume(delta string) error {
	p.pending += delta
	for {
		idx := strings.IndexByte(p.pending, '\n')
		if idx < 0 {
			return p.checkPendingSize()
		}
		line := p.pending[:idx+1]
		p.pending = p.pending[idx+1:]
		if err := p.processLine(line); err != nil {
			return err
		}
	}
}

func (p *stepParser) processLine(line string) error {
	lineText := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	trimmed := strings.TrimSpace(lineText)
	if !p.inCodeBlock && lineText == p.boundary {
		return p.commit(false)
	}

	p.current.WriteString(line)
	if err := p.checkCurrentSize(); err != nil {
		return err
	}
	if isFenceLine(trimmed) {
		p.inCodeBlock = !p.inCodeBlock
	}
	return nil
}

func (p *stepParser) finish() ([]StepPacket, error) {
	if p.pending != "" {
		p.current.WriteString(p.pending)
		p.pending = ""
		if err := p.checkCurrentSize(); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(p.current.String()) != "" {
		if err := p.commit(true); err != nil {
			return nil, err
		}
	}
	return append([]StepPacket(nil), p.steps...), nil
}

func (p *stepParser) commit(recovered bool) error {
	text := p.current.String()
	p.current.Reset()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	step := StepPacket{
		SchemaVersion: "streamma.step.v1",
		RunID:         p.config.RunID,
		AgentID:       p.config.AgentID,
		StepID:        fmt.Sprintf("%s:%d", p.config.AgentID, p.nextIndex),
		StepIndex:     p.nextIndex,
		Attempt:       1,
		Content: StepContent{
			Format:              "text",
			Text:                text,
			PublicReasoningOnly: true,
		},
		Boundary: StepBoundary{
			SourceSentinel:    p.boundary,
			Closed:            true,
			BoundaryRecovered: recovered,
		},
		Usage: p.usage,
		Dependencies: StepDependencies{
			InputEvents: append([]string(nil), p.config.InputEvents...),
		},
	}
	p.steps = append(p.steps, step)
	p.nextIndex++
	if p.emit != nil {
		return p.emit(cloneStepPacket(step))
	}
	return nil
}

func (p *stepParser) checkPendingSize() error {
	if p.config.MaxStepBytes <= 0 {
		return nil
	}
	if p.current.Len()+len(p.pending) <= p.config.MaxStepBytes {
		return nil
	}
	return &ParserFatalError{
		AgentID: p.config.AgentID,
		Reason:  fmt.Sprintf("step exceeded max bytes: %d", p.config.MaxStepBytes),
	}
}

func (p *stepParser) checkCurrentSize() error {
	if p.config.MaxStepBytes <= 0 || p.current.Len() <= p.config.MaxStepBytes {
		return nil
	}
	return &ParserFatalError{
		AgentID: p.config.AgentID,
		Reason:  fmt.Sprintf("step exceeded max bytes: %d", p.config.MaxStepBytes),
	}
}

func isFenceLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```")
}
