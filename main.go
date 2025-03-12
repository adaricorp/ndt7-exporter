// ndt7_exporter is an ndt7 non-interactive prometheus exporting client
// based on the m-lab ndt7-prometheus-exporter:
//
//	https://github.com/m-lab/ndt7-client-go/tree/main/cmd/ndt7-prometheus-exporter
//
// Usage:
//
//	ndt7_exporter
//
// The default behavior is for ndt7-client to discover a suitable server using
// Measurement Lab's locate service. This behavior may be overridden using
// either the `-server` or `-service-url` flags.
//
// The `-server <name>` flag specifies the server `name` for performing
// the ndt7 test. This option overrides `-service-url`.
//
// The `-service-url <url>` flag specifies a complete URL that specifies the
// scheme (e.g. "ws"), server name and port, protocol (e.g. /ndt/v7/download),
// and HTTP parameters. By default, upload and download measurements are run
// automatically. The `-service-url` specifies only one measurement direction.
//
// The `-no-verify` flag allows to skip TLS certificate verification.
//
// The `-scheme <scheme>` flag allows to override the default scheme, i.e.,
// "wss", with another scheme. The only other supported scheme is "ws"
// and causes ndt7 to run unencrypted.
//
// The `-timeout <string>` flag specifies the time after which the
// whole test is interrupted. The `<string>` is a string suitable to
// be passed to time.ParseDuration, e.g., "15s". The default is a large
// enough value that should be suitable for common conditions.
//
// The `-port` flag starts an HTTP server to export summary results in a form
// that can be consumed by Prometheus (http://prometheus.io).
//
// The `-profile` flag defines the file where to write a CPU profile
// that later you can pass to `go tool pprof`. See https://blog.golang.org/pprof.
//
// Additionally, passing any unrecognized flag, such as `-help`, will
// cause ndt7-client to print a brief help message.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/adaricorp/ndt7-exporter/internal/emitter"
	"github.com/adaricorp/ndt7-exporter/internal/params"
	"github.com/adaricorp/ndt7-exporter/internal/runner"
	"github.com/m-lab/go/flagx"
	"github.com/m-lab/go/memoryless"
	"github.com/m-lab/ndt7-client-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	"golang.org/x/sys/cpu"
)

const (
	defaultTimeout = 55 * time.Second
)

var (
	ClientName  = "github.com/adaricorp/ndt7-exporter"
	flagProfile = flag.String("profile", "",
		"file where to store pprof profile (see https://blog.golang.org/pprof)")

	flagScheme = flagx.Enum{
		Options: []string{"wss", "ws"},
		Value:   defaultSchemeForArch(),
	}

	flagNoVerify = flag.Bool("no-verify", false, "skip TLS certificate verification")
	flagServer   = flag.String("server", "", "optional ndt7 server hostname")
	flagTimeout  = flag.Duration(
		"timeout", defaultTimeout, "time after which the test is aborted")
	flagService  = flagx.URL{}
	flagUpload   = flag.Bool("upload", true, "perform upload measurement")
	flagDownload = flag.Bool("download", true, "perform download measurement")
	flagSourceIP = flag.String("source-ip", "", "source IP to use for tests")

	// The flag values below implement rate limiting at the recommended rate
	flagPeriodMean = flag.Duration(
		"period_mean",
		6*time.Hour,
		"mean period, e.g. 6h, between speed tests, when running in daemon mode",
	)
	flagPeriodMin = flag.Duration(
		"period_min",
		36*time.Minute,
		"minimum period, e.g. 36m, between speed tests, when running in daemon mode",
	)
	flagPeriodMax = flag.Duration(
		"period_max",
		15*time.Hour,
		"maximum period, e.g. 15h, between speed tests, when running in daemon mode",
	)

	flagListen = flag.String(
		"listen",
		"localhost:9191",
		"Expose an HTTP server on this address to export prometheus metrics",
	)
)

func init() {
	flag.Var(
		&flagScheme,
		"scheme",
		`WebSocket scheme to use: either "wss" or "ws"`,
	)
	flag.Var(
		&flagService,
		"service-url",
		"Service URL specifies target hostname and other URL fields like access token. Overrides -server.",
	)
}

// defaultSchemeForArch returns the default WebSocket scheme to use, depending
// on the architecture we are running on. A CPU without native AES instructions
// will perform poorly if TLS is enabled.
func defaultSchemeForArch() string {
	if cpu.ARM64.HasAES || cpu.ARM.HasAES || cpu.X86.HasAES {
		return "wss"
	}
	return "ws"
}

func main() {
	flag.Parse()

	if *flagProfile != "" {
		log.Printf("warning: using -profile will reduce the performance")
		fp, err := os.Create(*flagProfile)
		if err != nil {
			log.Fatal(err)
		}
		err = pprof.StartCPUProfile(fp)
		if err != nil {
			log.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}

	// If a service URL is given, then only one direction is possible.
	if flagService.URL != nil && strings.Contains(flagService.URL.Path, params.DownloadURLPath) {
		*flagUpload = false
		*flagDownload = true
	} else if flagService.URL != nil && strings.Contains(flagService.URL.Path, params.UploadURLPath) {
		*flagUpload = true
		*flagDownload = false
	} else if flagService.URL != nil {
		fmt.Println("WARNING: ignoring unsupported service url")
		flagService.URL = nil
	}

	// Source IP validation
	var sourceIP net.IP
	if *flagSourceIP != "" {
		sourceIP = net.ParseIP(*flagSourceIP)
		if sourceIP == nil {
			fmt.Printf("WARNING: ignoring unparsed source IP: %s\n", *flagSourceIP)
		}
	}

	e := emitter.NewQuiet(emitter.NewHumanReadable())

	dlThroughput := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "ndt7",
			Name:      "download_throughput_bps",
			Help:      "m-lab ndt7 download speed in bits/s",
		},
		[]string{
			// client IP and remote server
			"client_ip",
			"server_ip",
		})
	prometheus.MustRegister(dlThroughput)
	dlLatency := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "ndt7",
			Name:      "download_latency_seconds",
			Help:      "m-lab ndt7 download latency time in seconds",
		},
		[]string{
			// client IP and remote server
			"client_ip",
			"server_ip",
		})
	prometheus.MustRegister(dlLatency)
	ulThroughput := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "ndt7",
			Name:      "upload_throughput_bps",
			Help:      "m-lab ndt7 upload speed in bits/s",
		},
		[]string{
			// client IP and remote server
			"client_ip",
			"server_ip",
		})
	prometheus.MustRegister(ulThroughput)
	ulLatency := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "ndt7",
			Name:      "upload_latency_seconds",
			Help:      "m-lab ndt7 upload latency time in seconds",
		},
		[]string{
			// client IP and remote server
			"client_ip",
			"server_ip",
		})
	prometheus.MustRegister(ulLatency)

	// The result gauge captures the result of the last test attempt.
	//
	// Since its value is a timestamp, the following PromQL expression will
	// give the most recent result for each upload and download test.
	//
	//     time() - topk(1, ndt7_result_timestamp_seconds) without (result)
	lastResultGauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "ndt7",
			Name:      "result_timestamp_seconds",
			Help:      "m-lab ndt7 test completion time in seconds since 1970-01-01",
		},
		[]string{
			// which test completed
			"test",
			// test result
			"result",
		})
	prometheus.MustRegister(lastResultGauge)

	e = emitter.NewPrometheus(e, dlThroughput, dlLatency, ulThroughput, ulLatency, lastResultGauge)
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		log.Fatal(http.ListenAndServe(*flagListen, nil))
	}()

	ticker, err := memoryless.NewTicker(
		context.Background(),
		memoryless.Config{
			Expected: *flagPeriodMean,
			Min:      *flagPeriodMin,
			Max:      *flagPeriodMax,
		})
	if err != nil {
		log.Fatalf("Failed to create memoryless.Ticker: %v", err)
	}
	defer ticker.Stop()

	r := runner.New(
		runner.RunnerOptions{
			Download: *flagDownload,
			Upload:   *flagUpload,
			Timeout:  *flagTimeout,
			ClientFactory: func() *ndt7.Client {
				c := ndt7.NewClient(ClientName, version.Version)
				c.ServiceURL = flagService.URL
				c.Server = *flagServer
				c.Scheme = flagScheme.Value
				c.Dialer.TLSClientConfig = &tls.Config{
					InsecureSkipVerify: *flagNoVerify,
				}
				if sourceIP != nil {
					c.Dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
						dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: sourceIP}}
						return dialer.DialContext(ctx, network, addr)
					}
				}

				return c
			},
		},
		e,
		ticker)

	r.RunTestsInLoop()
}
