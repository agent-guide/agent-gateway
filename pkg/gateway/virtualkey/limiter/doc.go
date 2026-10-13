// Package limiter owns the designated protected VirtualKey admission SPI.
// The designated surfaces are Dimension and its LLM/MCP/Agent constants,
// Policy, Clock, LimiterRequest, LimiterDecision, LimiterBackend, LimiterProvider,
// Config, ModuleID, MemoryBackendType, Factory, NewMemoryBackend and the
// MemoryBackend Admit/Retire/Health/Close methods, ErrLimiterUnavailable and
// ErrLimiterRetired. Their exported fields and method signatures are protected
// beginning with v0.7.0 after published-tag consumer acceptance.
// Selection and its accessors are Community assembly implementation surfaces.
//
// Policies and globally unique generations are committed before admission.
// One call atomically consumes one token or denies without consumption; errors
// can be ambiguous and are never retried by the gateway. Cancellation and bounded
// deadlines are mandatory. Retire permanently fences generations, even when a
// late operation arrives; Admit returns ErrLimiterRetired (possibly wrapped),
// which never triggers fail-open. Generation-scoped quota storage survives replacement
// of prepared backend connection handles on unrelated extension mutations.
// A switch of backend authority/config resets generations; no token transfer is
// promised. Prepare initializes handles, Health diagnoses, Close stops and drains
// once under the extension manager's common five-second lifecycle budget.
package limiter
