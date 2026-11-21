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
//	options    {name, conn?}                   → [Option]
//	shutdown   {}                              → {} (then the process exits)
//
// call methods (by capability):
//
//	switcher.state · switcher.preview {input} · switcher.cut · switcher.fade {durationMs}
//	slides.info · slides.actions · slides.do {action}
//	mixer.probe · mixer.maxChannel · mixer.getMute {channel} · mixer.setMute {channel, muted}
//	addon.state {settings?} · addon.action {name, params}
//	addon.resource {path}                     → {contentType, data, maxAge?} (apiVersion 2)
//
// addon.resource returns a file the add-on's state refers to as
// "res:<path>" (a slide picture, a thumbnail): data is base64, at most
// MaxResource (4 MB) decoded. A connection without a Resourcer answers
// CodeNoResources; a path it doesn't have, CodeNotFound.
//
// v2.1 (still apiVersion 2; older controllers ignore it): WidgetDef.Fields
// (per-widget settings), Field.Multiple and the field type "order";
// options may carry conn (a widget field's connection: the connection's
// Optioner answers first, then Spec.Options); addon.state may carry
// {settings} (the asking widget's settings, for a SettingsStater);
// addon.resource may answer maxAge (seconds to cache, 0 = never).
//
// apiVersion 2 (manifest): the widget kinds image, buttons, input, strip,
// text, clock, sections, slider and frame; WidgetDef.Size / MinPx /
// Entry; a ui/ folder in the package for frame widgets. initialize sends
// the controller's apiVersion; an add-on whose manifest needs a newer one
// refuses to start, and a controller drops widgets of kinds it doesn't
// know instead of refusing the add-on.
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
	CodeNoResources   = 1004 // addon.resource on a connection without a Resourcer
	CodeNotFound      = 1005 // addon.resource: no such path
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
