package replicate

// testutil shims the single prometheus/client_golang/prometheus/testutil helper
// (ToFloat64) still used by the internal metric assertions after the
// prometheus/client_golang → luxfi/metric migration (commit 8521ab5) dropped the
// prometheus dependency. luxfi/metric's Counter and Gauge both expose Get()
// float64, so reading a single-series metric value needs no external package.
var testutil = metricTestutil{}

type metricTestutil struct{}

// ToFloat64 returns the current value of a single-series counter or gauge.
func (metricTestutil) ToFloat64(m interface{ Get() float64 }) float64 { return m.Get() }
