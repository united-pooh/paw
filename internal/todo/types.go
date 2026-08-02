package todo

import "time"

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
)

type Item struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  Status `json:"status"`
}

type Snapshot struct {
	Explanation string    `json:"explanation,omitempty"`
	Items       []Item    `json:"items"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UpdateInput struct {
	Explanation string `json:"explanation,omitempty"`
	Items       []Item `json:"items"`
}

type UpdateResult struct {
	Accepted bool     `json:"accepted"`
	Snapshot Snapshot `json:"snapshot"`
}

func (s Snapshot) Clone() Snapshot {
	if s.Items != nil {
		s.Items = append([]Item{}, s.Items...)
	}
	return s
}

func (r UpdateResult) Clone() UpdateResult {
	r.Snapshot = r.Snapshot.Clone()
	return r
}

func (s Snapshot) CompletedCount() int {
	count := 0
	for _, item := range s.Items {
		if item.Status == StatusCompleted {
			count++
		}
	}
	return count
}

func (s Snapshot) TotalCount() int { return len(s.Items) }

func (s Snapshot) Cleared() bool { return len(s.Items) == 0 }

func (s Snapshot) AllCompleted() bool {
	return len(s.Items) > 0 && s.CompletedCount() == len(s.Items)
}
