package addon

import "encoding/json"

// The protocol: JSON-RPC 2.0, one JSON object per line, over the add-on
// process's stdin (controller → add-on) and stdout (add-on → controller).
// stderr is free text and lands in the controller's log.
//
// Controller → add-on requests:
//
//	initialize {apiVersion, host}              → {manifest}
//	open       {conn, type, config}            → {capabilities}
//	close      {conn}                          → {}
//	call       {conn, method, params}          → method's result
//	options    {name}                          → [Option]
//	shutdown   {}                              → {} (then the process exits)
//
// call methods (by capability):
//
//	switcher.state · switcher.preview {input} · switcher.cut · switcher.fade {durationMs}
//	slides.info · slides.actions · slides.do {action}
//	mixer.probe · mixer.maxChannel · mixer.getMute {channel} · mixer.setMute {channel, muted}
//	addon.state · addon.action {name, params}
//
// Add-on → controller notifications (no id):
//
//	log     {level, msg}     level: debug | info | warn | error
//	changed {conn}           the connection's state changed; widgets refresh

// Message is one JSON-RPC message (request, response or notification).
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

// Error codes beyond JSON-RPC's own.
const (
	CodeNotConfigured = 1001
	CodeUnknownAction = 1002
	CodeNoConnection  = 1003
)

type InitializeParams struct {
	APIVersion int    `json:"apiVersion"`
	Host       string `json:"host"`
}

type InitializeResult struct {
	Manifest Manifest `json:"manifest"`
}

type OpenParams struct {
	Conn   string          `json:"conn"`
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config"`
}

type OpenResult struct {
	Capabilities []string `json:"capabilities"`
}

type CallParams struct {
	Conn   string          `json:"conn"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type LogParams struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
}
