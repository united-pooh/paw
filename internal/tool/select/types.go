package selecttool

type Mode string

const (
	ModeSingle   Mode = "single"
	ModeMultiple Mode = "multiple"
)

type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type Request struct {
	ID                 string   `json:"id,omitempty"`
	Prompt             string   `json:"prompt"`
	Mode               Mode     `json:"mode"`
	Options            []Option `json:"options"`
	InitialSelectedIDs []string `json:"initial_selected_ids,omitempty"`
	MinSelect          int      `json:"min_select"`
	MaxSelect          int      `json:"max_select"`
}

func (r Request) Clone() Request {
	r.Options = append([]Option(nil), r.Options...)
	r.InitialSelectedIDs = append([]string(nil), r.InitialSelectedIDs...)
	return r
}

type Result struct {
	Cancelled   bool     `json:"cancelled"`
	SelectedIDs []string `json:"selected_ids"`
}

func (r Result) Clone() Result {
	if r.SelectedIDs == nil {
		r.SelectedIDs = nil
	} else {
		r.SelectedIDs = append([]string{}, r.SelectedIDs...)
	}
	return r
}

type EventKind uint8

const (
	EventRequest EventKind = iota + 1
	EventInvalidated
	EventClosed
)

type Event struct {
	Kind      EventKind
	Request   Request
	RequestID string
}
