package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/adaricorp/ndt7-exporter/internal/emitter"
	"github.com/adaricorp/ndt7-exporter/internal/mocks"
	"github.com/adaricorp/ndt7-exporter/internal/params"
	"github.com/m-lab/go/memoryless"
	"github.com/m-lab/go/testingx"
	"github.com/m-lab/locate/api/locate"
	"github.com/m-lab/ndt-server/ndt7/ndt7test"
	"github.com/m-lab/ndt7-client-go"
	"github.com/m-lab/ndt7-client-go/spec"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	ClientName    = "ndt7-client-go-cmd-runner-test"
	ClientVersion = "1.2.3"
)

type mockedEmitter struct {
	StartingError  error
	ConnectedError error
	CompleteError  error
}

func (me mockedEmitter) OnStarting(test spec.TestKind) error {
	return me.StartingError
}

func (mockedEmitter) OnError(test spec.TestKind, err error) error {
	return nil
}

func (me mockedEmitter) OnConnected(test spec.TestKind, fqdn string) error {
	return me.ConnectedError
}

func (mockedEmitter) OnDownloadEvent(m *spec.Measurement) error {
	return nil
}

func (mockedEmitter) OnUploadEvent(m *spec.Measurement) error {
	return nil
}

func (me mockedEmitter) OnComplete(test spec.TestKind) error {
	return me.CompleteError
}

func (me mockedEmitter) OnSummary(*emitter.Summary) error {
	return nil
}

func TestRunTestOnStartingError(t *testing.T) {
	runner := Runner{
		client: ndt7.NewClient(ClientName, ClientVersion),
		emitter: mockedEmitter{
			StartingError: errors.New("mocked error"),
		},
	}
	err := runner.runTest(
		context.Background(),
		"download",
		func(context.Context) (<-chan spec.Measurement, error) {
			out := make(chan spec.Measurement)
			close(out)
			return out, nil
		},
		func(m *spec.Measurement) error {
			return nil
		},
	)
	if err == nil {
		t.Fatal("expected error here")
	}
}

func TestRunTestOnConnectedError(t *testing.T) {
	runner := Runner{
		client: ndt7.NewClient(ClientName, ClientVersion),
		emitter: mockedEmitter{
			ConnectedError: errors.New("mocked error"),
		},
	}
	err := runner.runTest(
		context.Background(),
		"download",
		func(context.Context) (<-chan spec.Measurement, error) {
			out := make(chan spec.Measurement)
			close(out)
			return out, nil
		},
		func(m *spec.Measurement) error {
			return nil
		},
	)
	if err == nil {
		t.Fatal("expected error here")
	}
}

func TestRunTestOnCompleteError(t *testing.T) {
	runner := Runner{
		client: ndt7.NewClient(ClientName, ClientVersion),
		emitter: mockedEmitter{
			CompleteError: errors.New("mocked error"),
		},
	}
	err := runner.runTest(
		context.Background(),
		"download",
		func(context.Context) (<-chan spec.Measurement, error) {
			out := make(chan spec.Measurement)
			close(out)
			return out, nil
		},
		func(m *spec.Measurement) error {
			return nil
		},
	)
	if err == nil {
		t.Fatal("expected error here")
	}
}

func TestRunTestEmitEventError(t *testing.T) {
	runner := Runner{
		client:  ndt7.NewClient(ClientName, ClientVersion),
		emitter: mockedEmitter{},
	}
	err := runner.runTest(
		context.Background(),
		"download",
		func(context.Context) (<-chan spec.Measurement, error) {
			out := make(chan spec.Measurement)
			go func() {
				defer close(out)
				out <- spec.Measurement{}
			}()
			return out, nil
		},
		func(m *spec.Measurement) error {
			return errors.New("mocked error")
		},
	)
	if err == nil {
		t.Fatal("expected error here")
	}
}

func TestBatchEmitterEventsOrderNormal(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	// Create local ndt7test server.
	h, fs := ndt7test.NewNDT7Server(t)
	defer os.RemoveAll(h.DataDir)
	defer fs.Close()
	defer waitForArchivedResult(t, h.DataDir)
	u, err := url.Parse(fs.URL)
	testingx.Must(t, err, "failed to parse ndt7test server url")

	writer := &mocks.SavingWriter{}
	runner := Runner{
		client:  ndt7.NewClient(ClientName, ClientVersion),
		emitter: emitter.NewJSON(writer),
	}
	runner.client.Scheme = "ws"
	runner.client.Server = u.Host

	err = runner.runTest(
		context.Background(),
		"download",
		runner.client.StartDownload,
		runner.emitter.OnDownloadEvent,
	)
	testingx.Must(t, err, "failed to run test")
	numLines := len(writer.Data)
	if numLines < 4 {
		t.Fatal("expected at least four lines")
	}
	for lineno, data := range writer.Data {
		var m struct {
			Key string
		}
		err := json.Unmarshal(data, &m)
		if err != nil {
			t.Fatal(err)
		}
		if lineno == 0 {
			if m.Key != "starting" {
				t.Fatal("unexpected first key")
			}
		} else if lineno == 1 {
			if m.Key != "connected" {
				t.Fatal("unexpected second key")
			}
		} else if lineno < numLines-1 {
			if m.Key != "measurement" {
				t.Fatalf("expected measurement key at line: %d; found %s",
					lineno, m.Key)
			}
		} else if lineno == numLines-1 {
			if m.Key != "complete" {
				t.Fatal("unexpected last key")
			}
		} else {
			t.Fatal("invalid index")
		}
	}
}

func TestBatchEmitterEventsOrderFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	writer := &mocks.SavingWriter{}
	runner := Runner{
		client:  ndt7.NewClient(ClientName, ClientVersion),
		emitter: emitter.NewJSON(writer),
	}
	loc := locate.NewClient("fake-agent")
	loc.BaseURL = &url.URL{Path: "\t"}
	runner.client.Locate = loc
	err := runner.runTest(
		context.Background(),
		"download",
		runner.client.StartDownload,
		runner.emitter.OnDownloadEvent,
	)
	if err == nil {
		t.Fatal("expected error here")
	}
	numLines := len(writer.Data)
	if numLines != 3 {
		t.Fatal("expected at exactly three lines")
	}
	for lineno, data := range writer.Data {
		var m struct {
			Key string
		}
		err := json.Unmarshal(data, &m)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%d - %s\n", lineno, m.Key)
		if lineno == 0 {
			if m.Key != "starting" {
				t.Fatal("unexpected first key")
			}
		} else if lineno == 1 {
			if m.Key != "error" {
				t.Fatal("unexpected second key")
			}
		} else if lineno == 2 {
			if m.Key != "complete" {
				t.Fatal("unexpected third key")
			}
		} else {
			t.Fatal("invalid index")
		}
	}
}

// We hijack the channel in a memoryless.Ticker to allow us to count the
// number of ticks consumed (and skip the waits).
type countingTicker struct {
	ticker    *memoryless.Ticker
	waitCount int
	writeChan chan<- time.Time
	countChan chan int
}

func newCountingTicker(countChan chan int) *countingTicker {
	c := make(chan time.Time)
	ticker := &countingTicker{
		ticker:    &memoryless.Ticker{C: c},
		waitCount: 0,
		writeChan: c,
		countChan: countChan,
	}
	go func() {
		for {
			ticker.writeChan <- time.Now()
			ticker.waitCount++
			ticker.countChan <- ticker.waitCount
		}
	}()
	return ticker
}

func TestRunTestsInLoopDaemon(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}

	// Create local ndt7test server.
	h, fs := ndt7test.NewNDT7Server(t)
	defer os.RemoveAll(h.DataDir)
	defer fs.Close()
	u, err := url.Parse(fs.URL)
	testingx.Must(t, err, "failed to parse ndt7test server url")
	// Setup flags to use the service-url option.
	url := &url.URL{
		Scheme: "ws",
		Host:   u.Host,
		Path:   params.DownloadURLPath,
	}

	ch := make(chan int)
	ticker := newCountingTicker(ch)

	runner := Runner{
		emitter: mockedEmitter{},
		ticker:  ticker.ticker,
		opt: RunnerOptions{
			Download: false, // skip download test
			Upload:   false, // skip upload test
			Timeout:  55 * time.Second,
			ClientFactory: func() *ndt7.Client {
				client := ndt7.NewClient(ClientName, ClientVersion)
				client.ServiceURL = url
				client.Server = u.Host
				client.Scheme = "ws"

				return client
			},
		},
	}

	go runner.RunTestsInLoop()
	// Test that daemon mode calls uses ticker to wait in a loop
	if c := <-ch; c != 1 {
		t.Errorf("unexpected count of Wait() calls: got %d", c)
	}
	if c := <-ch; c != 2 {
		t.Errorf("unexpected count of Wait() calls: got %d", c)
	}
}

func TestMakeSummary(t *testing.T) {
	// Simulate a 1% retransmission rate and a 10ms RTT.
	tcpInfo := &spec.TCPInfo{}
	tcpInfo.BytesSent = 100
	tcpInfo.BytesRetrans = 1
	tcpInfo.MinRTT = 10000
	// Simulate a 8Mb/s upload rate.
	tcpInfo.BytesReceived = 10000000
	tcpInfo.ElapsedTime = 10000000

	results := map[spec.TestKind]*ndt7.LatestMeasurements{
		spec.TestDownload: {
			Client: spec.Measurement{
				AppInfo: &spec.AppInfo{
					NumBytes:    100,
					ElapsedTime: 1,
				},
			},
			ConnectionInfo: &spec.ConnectionInfo{
				Client: "127.0.0.1:12345",
				Server: "127.0.0.2:443",
				UUID:   "test-download-uuid",
			},
			Server: spec.Measurement{
				TCPInfo: tcpInfo,
			},
		},
		spec.TestUpload: {
			Server: spec.Measurement{
				TCPInfo: tcpInfo,
			},
			ConnectionInfo: &spec.ConnectionInfo{
				Client: "127.0.0.1:12345",
				Server: "127.0.0.2:443",
				UUID:   "test-upload-uuid",
			},
		},
	}

	expected := &emitter.Summary{
		ServerFQDN: "test",
		ClientIP:   "127.0.0.1",
		ServerIP:   "127.0.0.2",
		Download: &emitter.SubtestSummary{
			UUID: "test-download-uuid",
			Throughput: emitter.ValueUnitPair{
				Value: 800.0,
				Unit:  "Mbit/s",
			},
			Latency: emitter.ValueUnitPair{
				Value: 10.0,
				Unit:  "ms",
			},
			Retransmission: emitter.ValueUnitPair{
				Value: 1.0,
				Unit:  "%",
			},
		},
		Upload: &emitter.SubtestSummary{
			UUID: "test-upload-uuid",
			Throughput: emitter.ValueUnitPair{
				Value: 8.0,
				Unit:  "Mbit/s",
			},
			Latency: emitter.ValueUnitPair{
				Value: 10.0,
				Unit:  "ms",
			},
		},
	}

	generated := makeSummary("test", results)

	if !reflect.DeepEqual(generated, expected) {
		t.Errorf("expected %+v; got %+v", expected, generated)
		t.Fatal("makeSummary(): unexpected summary data")
	}
}

func TestMakeSummarySkippedDirection(t *testing.T) {
	result := &ndt7.LatestMeasurements{
		ConnectionInfo: &spec.ConnectionInfo{
			Client: "127.0.0.1:12345",
			Server: "127.0.0.2:443",
		},
	}
	tests := []struct {
		name                 string
		results              map[spec.TestKind]*ndt7.LatestMeasurements
		wantDownload, wantUp bool
	}{
		{
			name:         "download only",
			results:      map[spec.TestKind]*ndt7.LatestMeasurements{spec.TestDownload: result},
			wantDownload: true,
		},
		{
			name:    "upload only",
			results: map[spec.TestKind]*ndt7.LatestMeasurements{spec.TestUpload: result},
			wantUp:  true,
		},
		{
			name:    "neither",
			results: map[spec.TestKind]*ndt7.LatestMeasurements{},
		},
		{
			name: "nil entries",
			results: map[spec.TestKind]*ndt7.LatestMeasurements{
				spec.TestDownload: nil,
				spec.TestUpload:   nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s *emitter.Summary
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("makeSummary() panicked: %v", r)
					}
				}()
				s = makeSummary("test", tt.results)
			}()
			if got := s.Download != nil; got != tt.wantDownload {
				t.Errorf("Download present = %v, want %v", got, tt.wantDownload)
			}
			if got := s.Upload != nil; got != tt.wantUp {
				t.Errorf("Upload present = %v, want %v", got, tt.wantUp)
			}
		})
	}
}

// gaugeSeries returns every series g currently exports, keyed by its labels
// rendered as "name=value,name=value".
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

// summaryRecorder passes every event on to the wrapped Emitter and keeps the
// last summary, so a test can compare what was exported with what was
// measured.
type summaryRecorder struct {
	emitter.Emitter
	last *emitter.Summary
}

func (r *summaryRecorder) OnSummary(s *emitter.Summary) error {
	r.last = s
	return r.Emitter.OnSummary(s)
}

// exporterMetrics is the emitter chain the exporter builds, around gauges
// shaped like the ones it registers. The gauges are not registered, so each
// test gets its own.
type exporterMetrics struct {
	emitter    emitter.Emitter
	tp, lat    map[spec.TestKind]*prometheus.GaugeVec
	lastResult *prometheus.GaugeVec
	summaries  *summaryRecorder
}

func newExporterMetrics() exporterMetrics {
	gauge := func(name string, labels ...string) *prometheus.GaugeVec {
		return prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Namespace: "ndt7", Name: name, Help: name},
			labels,
		)
	}
	m := exporterMetrics{
		tp: map[spec.TestKind]*prometheus.GaugeVec{
			spec.TestDownload: gauge("download_throughput_bps", "client_ip", "server_ip"),
			spec.TestUpload:   gauge("upload_throughput_bps", "client_ip", "server_ip"),
		},
		lat: map[spec.TestKind]*prometheus.GaugeVec{
			spec.TestDownload: gauge("download_latency_seconds", "client_ip", "server_ip"),
			spec.TestUpload:   gauge("upload_latency_seconds", "client_ip", "server_ip"),
		},
		lastResult: gauge("result_timestamp_seconds", "test", "result"),
		summaries: &summaryRecorder{
			Emitter: emitter.NewQuiet(emitter.NewHumanReadableWithWriter(&mocks.SavingWriter{})),
		},
	}
	m.emitter = emitter.NewPrometheus(
		m.summaries,
		m.tp[spec.TestDownload], m.lat[spec.TestDownload],
		m.tp[spec.TestUpload], m.lat[spec.TestUpload],
		m.lastResult,
	)
	return m
}

// checkSkippedAbsent fails the test if the direction that was not run
// exports any series at all.
func checkSkippedAbsent(t *testing.T, m exporterMetrics, skipped spec.TestKind) {
	t.Helper()
	for _, g := range []*prometheus.GaugeVec{m.tp[skipped], m.lat[skipped]} {
		if got := gaugeSeries(t, g); len(got) != 0 {
			t.Errorf("skipped %s exports %v, want nothing", skipped, got)
		}
	}
	for series := range gaugeSeries(t, m.lastResult) {
		if strings.Contains(series, "test="+string(skipped)) {
			t.Errorf("result_timestamp_seconds has %q for a test that never ran", series)
		}
	}
}

// runTestsOnce calls RunTestsOnce and turns a panic into a test failure, so
// a regression reports which case broke instead of killing the test binary.
func runTestsOnce(t *testing.T, r *Runner) []error {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("RunTestsOnce() panicked: %v", p)
		}
	}()
	return r.RunTestsOnce()
}

// singleDirectionCases runs each direction on its own.
var singleDirectionCases = []struct {
	name         string
	ran, skipped spec.TestKind
}{
	{"download only", spec.TestDownload, spec.TestUpload},
	{"upload only", spec.TestUpload, spec.TestDownload},
}

// waitForArchivedResult waits for the ndt7test server to write the result
// file it archives under dataDir at the end of every test. The server writes
// it after the client has hung up, from a handler that closing the server
// does not wait for (the websocket connection is hijacked), and it exits the
// whole test binary if dataDir has been removed by then. Defer it after the
// server's own cleanup, so that it runs first. It gives up quietly when no
// file appears, since a test that never reached the server has none to wait
// for.
func waitForArchivedResult(t *testing.T, dataDir string) {
	t.Helper()
	pattern := filepath.Join(dataDir, "ndt7", "*", "*", "*", "*")
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		// Once the file exists the server only writes to the open file, so
		// removing the directory is safe from then on.
		if found, _ := filepath.Glob(pattern); len(found) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("the ndt7test server archived no result under %s", dataDir)
}

// closedAddr returns a local address that refuses connections, so a test
// run against it fails at once without moving any data.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// TestRunTestsOnceSingleDirection runs one direction through the same
// emitter chain the exporter builds, against a port that refuses the
// connection, and checks that the direction left out exports nothing at all.
func TestRunTestsOnceSingleDirection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	for _, tt := range singleDirectionCases {
		t.Run(tt.name, func(t *testing.T) {
			m := newExporterMetrics()
			addr := closedAddr(t)
			rn := New(RunnerOptions{
				Download: tt.ran == spec.TestDownload,
				Upload:   tt.ran == spec.TestUpload,
				Timeout:  10 * time.Second,
				ClientFactory: func() *ndt7.Client {
					c := ndt7.NewClient(ClientName, ClientVersion)
					c.Scheme = "ws"
					c.Server = addr
					return c
				},
			}, m.emitter, nil)

			// The direction that ran could not connect, and nothing else failed.
			if errs := runTestsOnce(t, rn); len(errs) != 1 {
				t.Fatalf("RunTestsOnce() errors = %v, want exactly one", errs)
			}

			checkSkippedAbsent(t, m, tt.skipped)
			wantErr := "result=ERROR,test=" + string(tt.ran)
			results := gaugeSeries(t, m.lastResult)
			if _, ok := results[wantErr]; !ok {
				t.Errorf("result_timestamp_seconds = %v, want a %q series", results, wantErr)
			}
		})
	}
}

// TestRunTestsOnceSingleDirectionMeasured runs one direction for real
// against a local ndt7 server, through the same emitter chain the exporter
// builds, and checks that it exports what it measured under the real client
// and server addresses while the direction left out exports nothing at all.
func TestRunTestsOnceSingleDirectionMeasured(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	// The ndt7test server reads the kernel's TCP info only on Linux. On
	// other systems its measurements carry none, so the upload throughput
	// (read at the server) and both latencies are zero there, and only the
	// download throughput (read at the client) is known to be positive.
	serverHasTCPInfo := runtime.GOOS == "linux"

	for _, tt := range singleDirectionCases {
		t.Run(tt.name, func(t *testing.T) {
			// Each direction takes the full ndt7 test length; run them
			// side by side, each against its own server.
			t.Parallel()
			// The server's data directory is a t.TempDir, removed after
			// the deferred calls below have run.
			h, fs := ndt7test.NewNDT7Server(t)
			defer fs.Close()
			defer waitForArchivedResult(t, h.DataDir)
			u, err := url.Parse(fs.URL)
			testingx.Must(t, err, "failed to parse ndt7test server url")

			m := newExporterMetrics()
			rn := New(RunnerOptions{
				Download: tt.ran == spec.TestDownload,
				Upload:   tt.ran == spec.TestUpload,
				Timeout:  55 * time.Second,
				ClientFactory: func() *ndt7.Client {
					c := ndt7.NewClient(ClientName, ClientVersion)
					c.Scheme = "ws"
					c.Server = u.Host
					return c
				},
			}, m.emitter, nil)

			if errs := runTestsOnce(t, rn); len(errs) != 0 {
				t.Fatalf("RunTestsOnce() errors = %v, want none", errs)
			}

			s := m.summaries.last
			if s == nil {
				t.Fatal("no summary was emitted")
			}
			st := s.Download
			if tt.ran == spec.TestUpload {
				st = s.Upload
			}
			if st == nil {
				t.Fatalf("summary %+v has no %s result", s, tt.ran)
			}
			for _, ip := range []string{s.ClientIP, s.ServerIP} {
				if parsed := net.ParseIP(ip); parsed == nil || !parsed.IsLoopback() {
					t.Errorf("summary address %q is not a loopback IP", ip)
				}
			}

			// Exactly one series per gauge, under the addresses the test
			// really used, carrying the value that was measured.
			labels := "client_ip=" + s.ClientIP + ",server_ip=" + s.ServerIP
			tpName, latName := string(tt.ran)+" throughput", string(tt.ran)+" latency"
			tp, tpOK := checkOneSeries(t, tpName, m.tp[tt.ran], labels, st.Throughput.Value*1e6)
			lat, latOK := checkOneSeries(t, latName, m.lat[tt.ran], labels, st.Latency.Value/1e3)
			if tpOK && (tt.ran == spec.TestDownload || serverHasTCPInfo) && tp <= 0 {
				t.Errorf("%s = %v bit/s, want > 0", tpName, tp)
			}
			if latOK && serverHasTCPInfo && lat <= 0 {
				t.Errorf("%s = %v s, want > 0", latName, lat)
			}

			checkSkippedAbsent(t, m, tt.skipped)
			wantOK := "result=OK,test=" + string(tt.ran)
			results := gaugeSeries(t, m.lastResult)
			if _, ok := results[wantOK]; len(results) != 1 || !ok {
				t.Errorf("result_timestamp_seconds = %v, want only a %q series", results, wantOK)
			}
		})
	}
}

// checkOneSeries fails the test unless g exports exactly one series, with
// the given labels and a value within rounding of want. It returns that
// series' value, and false when there is no such single series.
func checkOneSeries(t *testing.T, name string, g *prometheus.GaugeVec, labels string, want float64) (float64, bool) {
	t.Helper()
	got := gaugeSeries(t, g)
	v, ok := got[labels]
	if len(got) != 1 || !ok {
		t.Errorf("%s exports %v, want exactly one series, labelled %q", name, got, labels)
		return 0, false
	}
	if math.Abs(v-want) > 1e-9*math.Abs(want) {
		t.Errorf("%s {%s} = %v, want the measured %v", name, labels, v, want)
	}
	return v, true
}
