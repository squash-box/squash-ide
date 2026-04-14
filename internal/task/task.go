package task

// Task represents a parsed task from the vault.
type Task struct {
	ID       string   `json:"id" yaml:"id"`
	Type     string   `json:"type" yaml:"type"`
	Title    string   `json:"title" yaml:"title"`
	Project  string   `json:"project" yaml:"project"`
	Status   string   `json:"status" yaml:"status"`
	Created  string   `json:"created" yaml:"created"`
	Priority string   `json:"priority" yaml:"priority"`
	Related  []string `json:"related,omitempty" yaml:"related"`
	Body     string   `json:"body" yaml:"-"`
}
