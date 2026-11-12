package domain

import "time"

type ProcessSpec struct {
	Directory   string   `json:"directory"`
	Argv        []string `json:"argv"`
	Interactive bool     `json:"interactive,omitempty"`
}

// OutputChunk is bounded to 4096 bytes. JSON encodes Data as base64 so binary
// output cannot corrupt structured stdout or require lossy text conversion.
type OutputChunk struct {
	Stream   string `json:"stream"`
	Encoding string `json:"encoding"`
	Data     []byte `json:"data"`
}

type ProcessResult struct {
	OutputCaptured  bool          `json:"output_captured"`
	Argv            []string      `json:"argv"`
	ExitCode        int           `json:"exit_code"`
	Duration        time.Duration `json:"duration_ns"`
	Cancelled       bool          `json:"cancelled"`
	Signal          string        `json:"signal,omitempty"`
	Encoding        string        `json:"encoding"`
	Stdout          []byte        `json:"stdout"`
	Stderr          []byte        `json:"stderr"`
	StdoutTruncated bool          `json:"stdout_truncated"`
	StderrTruncated bool          `json:"stderr_truncated"`
}

// ProcessSummary is safe to retain in journals without duplicating child output.
type ProcessSummary struct {
	ExitCode  int           `json:"exit_code"`
	Duration  time.Duration `json:"duration_ns"`
	Cancelled bool          `json:"cancelled"`
	Signal    string        `json:"signal,omitempty"`
}

func (r ProcessResult) Summary() *ProcessSummary {
	return &ProcessSummary{ExitCode: r.ExitCode, Duration: r.Duration, Cancelled: r.Cancelled, Signal: r.Signal}
}
