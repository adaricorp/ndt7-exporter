package emitter

import (
	"time"

	"github.com/m-lab/ndt7-client-go/spec"
	"github.com/prometheus/client_golang/prometheus"
)

// Prometheus tees summary metrics as prometheus metrics.
// The message is actually emitted by the embedded Emitter.
type Prometheus struct {
	emitter Emitter
	// Download throughput
	// Value: throughput in bits/s
	// Labels: client_ip, server_ip
	dlTp *prometheus.GaugeVec
	// Download latency
	// Value: latency in secs
	// Labels: client_ip, server_ip
	dlLat *prometheus.GaugeVec
	// Upload throughput
	// Value: throughput in bits/s
	// Labels: client_ip, server_ip
	ulTp *prometheus.GaugeVec
	// Upload latency
	// Value: latency in secs
	// Labels: client_ip, server_ip
	ulLat *prometheus.GaugeVec
	// Last results
	// Value: time in seconds since unix epoch
	// labels: test, result
	lastResult *prometheus.GaugeVec
}

// NewPrometheus returns a Summary emitter which emits messages
// via the passed Emitter.
func NewPrometheus(e Emitter, dlThroughput, dlLatency, ulThroughput, ulLatency, lastResult *prometheus.GaugeVec) Emitter {
	return &Prometheus{e, dlThroughput, dlLatency, ulThroughput, ulLatency, lastResult}
}

// OnStarting emits the starting event
func (p Prometheus) OnStarting(test spec.TestKind) error {
	return p.emitter.OnStarting(test)
}

// OnError emits the error event
func (p Prometheus) OnError(test spec.TestKind, err error) error {
	g := p.lastResult.WithLabelValues(string(test), "ERROR")
	g.Set(float64(time.Now().Unix()))
	return p.emitter.OnError(test, err)
}

// OnConnected emits the connected event
func (p Prometheus) OnConnected(test spec.TestKind, fqdn string) error {
	return p.emitter.OnConnected(test, fqdn)
}

// OnDownloadEvent handles an event emitted during the download
func (p Prometheus) OnDownloadEvent(m *spec.Measurement) error {
	return p.emitter.OnDownloadEvent(m)
}

// OnUploadEvent handles an event emitted during the upload
func (p Prometheus) OnUploadEvent(m *spec.Measurement) error {
	return p.emitter.OnUploadEvent(m)
}

// OnComplete is the event signalling the end of the test
func (p Prometheus) OnComplete(test spec.TestKind) error {
	g := p.lastResult.WithLabelValues(string(test), "OK")
	g.Set(float64(time.Now().Unix()))
	return p.emitter.OnComplete(test)
}

// OnSummary handles the summary event, emitted after the test is over.
//
// A direction that was not run (disabled with -download=false or
// -upload=false, or left out because -service-url names the other one) has
// a nil entry in the summary. Its gauges are reset and left empty, so that
// direction is absent from the scrape: it neither reports a zero it never
// measured nor keeps a value from an earlier run. The direction that did
// run is exported as usual.
func (p *Prometheus) OnSummary(s *Summary) error {
	exportSubtest(p.dlTp, p.dlLat, s.ClientIP, s.ServerIP, s.Download)
	exportSubtest(p.ulTp, p.ulLat, s.ClientIP, s.ServerIP, s.Upload)

	return p.emitter.OnSummary(s)
}

// exportSubtest replaces the throughput and latency series of one direction
// with the values in st, or clears them when st is nil (direction not run).
func exportSubtest(tp, lat *prometheus.GaugeVec, clientIP, serverIP string, st *SubtestSummary) {
	tp.Reset()
	lat.Reset()
	if st == nil {
		return
	}
	// Note this assumes throughput units are Mbit/s and latency units are
	// msecs.
	tp.WithLabelValues(clientIP, serverIP).Set(st.Throughput.Value * 1000.0 * 1000.0)
	lat.WithLabelValues(clientIP, serverIP).Set(st.Latency.Value / 1000.0)
}
