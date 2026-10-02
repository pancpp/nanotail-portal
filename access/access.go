package access

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/pancpp/nanotail-portal/conf"
)

const (
	CHECK_INTERVAL  = 10 * time.Second
	REPORT_INTERVAL = 600 * time.Second
)

var (
	gDeviceID      string
	gReportService *ReportService
)

type ReportService struct {
	reporter       *IPReporter
	interfaceName  string
	lastReportTime time.Time

	hostname string
	ipv4     string
	ipv6     string
}

// Init prepares reporting and caches the device ID. Call it once at startup,
// before starting HTTP handlers or the reporting worker.
func Init(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	if !conf.GetBool("access_enable") {
		log.Println("(access) Access is disabled.")
		return nil
	}

	// Read DeviceID and DeviceSig together from vendor storage.
	deviceID, deviceSig, err := GetDeviceCredentials()
	if err != nil {
		log.Println("(access) get device credentials err:", err)
		return err
	}

	// Create reporter
	reporter, err := NewIPReporter(conf.GetString("access_api_prefix"), deviceID, deviceSig)
	if err != nil {
		log.Println("(access) create IP reporter err:", err)
		return err
	}

	// Get ETH interface name
	ethName := conf.GetString("access_eth_name")

	// Set reporter and eth name
	gReportService = &ReportService{
		reporter:       reporter,
		interfaceName:  ethName,
		lastReportTime: time.Unix(0, 0),
	}

	gDeviceID = deviceID

	return nil
}

// Run reports until ctx is canceled. The caller owns the goroutine and must
// wait for Run to return before completing shutdown.
func Run(ctx context.Context) {
	if gReportService == nil {
		return
	}
	// Each runtime starts with fresh observations, including after an aborted reset.
	service := *gReportService
	service.Run(ctx)
}

// GetDeviceID returns the cached ID, or an empty string before initialization.
func GetDeviceID() string {
	return gDeviceID
}

func (r *ReportService) Run(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	// Check IP/Hostname every CHECK_INTERVAL
	// Report immediately if anything changed or after REPORT_INTERVAL
	// Do report immediately at beginning
	if err := r.doReport(ctx); err != nil {
		log.Println("(access) report with error:", err)
	} else {
		r.lastReportTime = time.Now()
	}

	ticker := time.NewTicker(CHECK_INTERVAL)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			r.doReport(ctx)
		}
	}
}

func (r *ReportService) doReport(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hostname, err := os.Hostname()
	if err != nil {
		log.Println("(access) get hostname err:", err)
		hostname = ""
	}

	ipv4, err := GetIPv4(r.interfaceName)
	if err != nil {
		log.Println("(access) get IPv4 err:", err)
		ipv4 = ""
	}

	ipv6, err := GetIPv6(r.interfaceName)
	if err != nil {
		log.Println("(access) get IPv6 err:", err)
		ipv6 = ""
	}

	return r.reportNetwork(ctx, hostname, ipv4, ipv6)
}

func (r *ReportService) reportNetwork(ctx context.Context, hostname, ipv4, ipv6 string) error {
	if time.Since(r.lastReportTime) > REPORT_INTERVAL ||
		(hostname != "" && hostname != r.hostname) ||
		(ipv4 != "" && ipv4 != r.ipv4) ||
		(ipv6 != "" && ipv6 != r.ipv6) {
		if err := r.reporter.Report(ctx, hostname, ipv4, ipv6); err != nil {
			log.Println("(access) report IPs and hostname err:", err)
			return err
		}
		r.lastReportTime = time.Now()
	}

	// Remember empty observations too, so an address returning with the same
	// value triggers a report. Failed requests return before this state changes.
	r.hostname, r.ipv4, r.ipv6 = hostname, ipv4, ipv6

	return nil
}
