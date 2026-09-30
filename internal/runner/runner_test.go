package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"reflect"
	"slices"
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

// gaugeSeries returns the label sets g currently exports, each rendered as
// "name=value,name=value".
func gaugeSeries(t *testing.T, g *prometheus.GaugeVec) []string {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(g)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	series := []string{}
	for _, family := range families {
		for _, m := range family.GetMetric() {
			labels := []string{}
			for _, l := range m.GetLabel() {
				labels = append(labels, l.GetName()+"="+l.GetValue())
			}
			sort.Strings(labels)
			series = append(series, strings.Join(labels, ","))
		}
	}
	sort.Strings(series)
	return series
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
// emitter chain the exporter builds, and checks that the direction left
// out exports nothing at all.
func TestRunTestsOnceSingleDirection(t *testing.T) {
	tests := []struct {
		name             string
		download, upload bool
		ran, skipped     spec.TestKind
	}{
		{"download only", true, false, spec.TestDownload, spec.TestUpload},
		{"upload only", false, true, spec.TestUpload, spec.TestDownload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gauge := func(name string) *prometheus.GaugeVec {
				return prometheus.NewGaugeVec(
					prometheus.GaugeOpts{Namespace: "ndt7", Name: name, Help: name},
					[]string{"client_ip", "server_ip"},
				)
			}
			dlTp, dlLat := gauge("download_throughput_bps"), gauge("download_latency_seconds")
			ulTp, ulLat := gauge("upload_throughput_bps"), gauge("upload_latency_seconds")
			lastResult := prometheus.NewGaugeVec(
				prometheus.GaugeOpts{Namespace: "ndt7", Name: "result_timestamp_seconds", Help: "r"},
				[]string{"test", "result"},
			)
			e := emitter.NewPrometheus(
				emitter.NewQuiet(emitter.NewHumanReadableWithWriter(&mocks.SavingWriter{})),
				dlTp, dlLat, ulTp, ulLat, lastResult,
			)

			addr := closedAddr(t)
			rn := New(RunnerOptions{
				Download: tt.download,
				Upload:   tt.upload,
				Timeout:  10 * time.Second,
				ClientFactory: func() *ndt7.Client {
					c := ndt7.NewClient(ClientName, ClientVersion)
					c.Scheme = "ws"
					c.Server = addr
					return c
				},
			}, e, nil)

			var errs []error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("RunTestsOnce() panicked: %v", r)
					}
				}()
				errs = rn.RunTestsOnce()
			}()
			// The direction that ran could not connect, and nothing else failed.
			if len(errs) != 1 {
				t.Fatalf("RunTestsOnce() errors = %v, want exactly one", errs)
			}

			gauges := map[spec.TestKind][]*prometheus.GaugeVec{
				spec.TestDownload: {dlTp, dlLat},
				spec.TestUpload:   {ulTp, ulLat},
			}
			for _, g := range gauges[tt.skipped] {
				if got := gaugeSeries(t, g); len(got) != 0 {
					t.Errorf("skipped %s exports %v, want nothing", tt.skipped, got)
				}
			}

			results := gaugeSeries(t, lastResult)
			for _, series := range results {
				if strings.Contains(series, "test="+string(tt.skipped)) {
					t.Errorf("result_timestamp_seconds has %q for a test that never ran", series)
				}
			}
			wantErr := "result=ERROR,test=" + string(tt.ran)
			if !slices.Contains(results, wantErr) {
				t.Errorf("result_timestamp_seconds = %v, want a %q series", results, wantErr)
			}
		})
	}
}
