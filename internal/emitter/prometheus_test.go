package emitter

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/adaricorp/ndt7-exporter/internal/mocks"
	"github.com/prometheus/client_golang/prometheus"
)

// newTestGaugeVec returns an unregistered gauge shaped like the ones the
// exporter registers for throughput and latency.
func newTestGaugeVec(name string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Namespace: "ndt7", Name: name, Help: name},
		[]string{"client_ip", "server_ip"},
	)
}

type testPrometheus struct {
	*Prometheus
	out *mocks.SavingWriter
}

func newTestPrometheus() testPrometheus {
	sw := &mocks.SavingWriter{}
	p := NewPrometheus(
		jsonEmitter{sw},
		newTestGaugeVec("download_throughput_bps"),
		newTestGaugeVec("download_latency_seconds"),
		newTestGaugeVec("upload_throughput_bps"),
		newTestGaugeVec("upload_latency_seconds"),
		prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Namespace: "ndt7", Name: "result_timestamp_seconds", Help: "r"},
			[]string{"test", "result"},
		),
	).(*Prometheus)
	return testPrometheus{p, sw}
}

// gaugeSeries returns every series g currently exports, keyed by its
// labels rendered as "name=value,name=value".
func gaugeSeries(t *testing.T, g *prometheus.GaugeVec) map[string]float64 {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(g)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	series := map[string]float64{}
	for _, family := range families {
		for _, m := range family.GetMetric() {
			labels := []string{}
			for _, l := range m.GetLabel() {
				labels = append(labels, l.GetName()+"="+l.GetValue())
			}
			sort.Strings(labels)
			series[strings.Join(labels, ",")] = m.GetGauge().GetValue()
		}
	}
	return series
}

// emitSummary calls OnSummary and turns a panic into a test failure, so a
// regression reports which case broke instead of killing the test binary.
func emitSummary(t *testing.T, p testPrometheus, s *Summary) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("OnSummary(%+v) panicked: %v", s, r)
		}
	}()
	if err := p.OnSummary(s); err != nil {
		t.Fatal(err)
	}
}

func TestPrometheusOnSummary(t *testing.T) {
	// An earlier run that measured both directions from another address,
	// so a series it leaves behind is recognisable as stale.
	previous := &Summary{
		ClientIP: "192.0.2.1",
		ServerIP: "198.51.100.1",
		Download: &SubtestSummary{
			Throughput: ValueUnitPair{Value: 1, Unit: "Mbit/s"},
			Latency:    ValueUnitPair{Value: 1000, Unit: "ms"},
		},
		Upload: &SubtestSummary{
			Throughput: ValueUnitPair{Value: 2, Unit: "Mbit/s"},
			Latency:    ValueUnitPair{Value: 2000, Unit: "ms"},
		},
	}
	download := &SubtestSummary{
		Throughput: ValueUnitPair{Value: 100, Unit: "Mbit/s"},
		Latency:    ValueUnitPair{Value: 250, Unit: "ms"},
	}
	upload := &SubtestSummary{
		Throughput: ValueUnitPair{Value: 50, Unit: "Mbit/s"},
		Latency:    ValueUnitPair{Value: 125, Unit: "ms"},
	}
	const labels = "client_ip=192.0.2.2,server_ip=198.51.100.2"
	downloadTp := map[string]float64{labels: 100e6}
	downloadLat := map[string]float64{labels: 0.25}
	uploadTp := map[string]float64{labels: 50e6}
	uploadLat := map[string]float64{labels: 0.125}
	absent := map[string]float64{}

	tests := []struct {
		name                string
		download, upload    *SubtestSummary
		wantDlTp, wantDlLat map[string]float64
		wantUlTp, wantUlLat map[string]float64
	}{
		{
			name:     "both directions",
			download: download, upload: upload,
			wantDlTp: downloadTp, wantDlLat: downloadLat,
			wantUlTp: uploadTp, wantUlLat: uploadLat,
		},
		{
			name:     "download only",
			download: download, upload: nil,
			wantDlTp: downloadTp, wantDlLat: downloadLat,
			wantUlTp: absent, wantUlLat: absent,
		},
		{
			name:     "upload only",
			download: nil, upload: upload,
			wantDlTp: absent, wantDlLat: absent,
			wantUlTp: uploadTp, wantUlLat: uploadLat,
		},
		{
			name:     "neither direction",
			download: nil, upload: nil,
			wantDlTp: absent, wantDlLat: absent,
			wantUlTp: absent, wantUlLat: absent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPrometheus()
			emitSummary(t, p, previous)
			emitSummary(t, p, &Summary{
				ServerFQDN: "ndt.example",
				ClientIP:   "192.0.2.2",
				ServerIP:   "198.51.100.2",
				Download:   tt.download,
				Upload:     tt.upload,
			})

			for _, g := range []struct {
				name string
				vec  *prometheus.GaugeVec
				want map[string]float64
			}{
				{"download throughput", p.dlTp, tt.wantDlTp},
				{"download latency", p.dlLat, tt.wantDlLat},
				{"upload throughput", p.ulTp, tt.wantUlTp},
				{"upload latency", p.ulLat, tt.wantUlLat},
			} {
				if got := gaugeSeries(t, g.vec); !reflect.DeepEqual(got, g.want) {
					t.Errorf("%s: got %v, want %v", g.name, got, g.want)
				}
			}

			// The summary still reaches the wrapped emitter.
			if len(p.out.Data) != 2 {
				t.Errorf("wrapped emitter got %d summaries, want 2", len(p.out.Data))
			}
		})
	}
}
