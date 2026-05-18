// Package webhooks helps receivers verify HMAC-SHA256 signed webhook deliveries
// from NIS.
//
// A NIS webhook POST carries these headers:
//
//	X-NIS-Event           Event type (e.g. "account.created")
//	X-NIS-Delivery        Per-attempt unique UUID (use this for idempotency)
//	X-NIS-Subscription    Subscription ID
//	X-NIS-Timestamp       Unix seconds at the time of dispatch
//	X-NIS-Signature       sha256=<hex>  HMAC-SHA256 of timestamp.body
//
// To verify, pass the request + the shared secret to Verify. Verify checks
// the timestamp tolerance (default 5 minutes) and the HMAC, returning a typed
// error so the caller can distinguish replay attacks, signature mismatches,
// and clock skew. See the Verify example for usage.
package webhooks
