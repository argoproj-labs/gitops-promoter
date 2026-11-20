// Package loadreport turns the request log of the test SCM server into a report of the SCM API load GitOps Promoter
// generates: how many requests each scenario costs, by route and by the budget they count against.
//
// It only understands GitHub's API, because that is the provider the load harness measures. The harness in
// internal/controller runs the scenarios and hands their requests to Analyze; this package holds no test logic, so
// the same analysis can be run over a request log captured any other way.
//
// # Time compression
//
// A steady state is measured with the controllers' periodic timers running Compression times faster than shipped (a
// 5m requeue becomes 30s at 10x), so that a few minutes of observation cover many cycles. Every periodic timer is
// compressed by the same factor, which makes the extrapolation independent of which timer drives a request: the
// rate at shipped intervals is the observed rate divided by the factor. Costs that do not follow a compressed timer
// (GitHub App token minting, installation lookups) are counted but not extrapolated.
//
// The requests of a steady state arrive in bursts, one per cycle, so an observation that begins or ends inside a
// burst would miscount by a fraction of a cycle. Analyze therefore weighs the requests near the edges of the
// observation less (see taper), which makes the rate of periodic traffic independent of where the observation
// starts. The request counts in a report are plain counts; only the rates are weighted.
package loadreport
