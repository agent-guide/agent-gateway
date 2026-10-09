# Protected limiter seam

Read the parent/root rules and enterprise-extension-contract.md section 11.6.
This package owns the public admission SPI; it must not import its parent
virtualkey package, dispatcher, distribution, gateway, or internal observability.
Domain IDs/policies contain no bearer credentials. Contract diagnostics are
bounded by the owner; never forward callback strings to HTTP/events.

Prepare selected backends before extension activation. Retain immutable policy
identity across unchanged settings and unrelated generations. Module connection
handles are generation-owned; quota storage is policy-generation-owned and must
survive their replacement. Retire permanently fences old operations. Never retry
uncertain consumes or silently switch to local buckets. Default 100 ms/closed;
caller cancellation, retirement cancellation and ErrLimiterRetired never fail open.
Retired admissions return the sentinel (possibly wrapped); conformance checks it. Memory is the
Community default, distributed production storage is out of scope.

Run the same public contracttest.Limiter suite for memory and the independent
consumer backend, plus parent/extension/dispatcher race tests and both real server
process tests. The exact candidate protected symbol list lives in doc.go.
