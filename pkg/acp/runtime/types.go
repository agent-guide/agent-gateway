package runtime

type TurnRequest struct {
	ThreadID        string            `json:"thread_id"`
	SessionID       string            `json:"session_id,omitempty"`
	Input           string            `json:"input"`
	CWD             string            `json:"cwd,omitempty"`
	Model           string            `json:"model,omitempty"`
	FreshSession    bool              `json:"fresh_session,omitempty"`
	ConfigOverrides map[string]string `json:"config_overrides,omitempty"`
}

type TurnEvent struct {
	Event      string `json:"-"`
	SessionID  string `json:"session_id,omitempty"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
	Message    string `json:"message,omitempty"`
}

type EventSink func(TurnEvent) error
