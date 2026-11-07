package zmap

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/scan"
)

// Pinned monitor.c emits 21 header columns but 22 data columns: column 19 is
// empty before sendto-fail-last-one-sec/sendto-fail-avg-per-sec. Keep this quirk.
const StatusHeader = "real-time,time-elapsed,time-remaining,percent-complete,hit-rate,active-send-threads,sent-total,sent-last-one-sec,sent-avg-per-sec,recv-success-total,recv-success-last-one-sec,recv-success-avg-per-sec,recv-total,recv-total-last-one-sec,recv-total-avg-per-sec,pcap-drop-total,drop-last-one-sec,drop-avg-per-sec,sendto-fail-total,sendto-fail-last-one-sec,sendto-fail-avg-per-sec"

type decoder struct{ statusHeader bool }

func (*decoder) Results(r io.Reader, sink func(scan.Finding) error) error {
	header := false
	err := backend.Records(r, func(line string) error {
		if !header {
			if line != Fields {
				return fmt.Errorf("unrecognized ZMap result header")
			}
			header = true
			return nil
		}
		reader := csv.NewReader(strings.NewReader(line))
		reader.FieldsPerRecord = 6
		row, err := reader.Read()
		if err != nil {
			return fmt.Errorf("malformed ZMap result: %w", err)
		}
		if (row[3] != "0" && row[3] != "1") || (row[5] != "0" && row[5] != "1") {
			return fmt.Errorf("invalid ZMap success/repeat flags")
		}
		if row[3] == "0" || row[5] == "1" {
			return nil
		}
		if row[4] != "synack" {
			return fmt.Errorf("ZMap success is not a TCP SYN-ACK")
		}
		ip, err := netip.ParseAddr(row[0])
		if err != nil || !ip.Is4() {
			return fmt.Errorf("invalid ZMap response address")
		}
		port, err := strconv.ParseUint(row[1], 10, 16)
		if err != nil {
			return fmt.Errorf("invalid ZMap target source port")
		}
		stamp, err := strconv.ParseUint(row[2], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid ZMap observation timestamp")
		}
		return sink(scan.Finding{Address: ip, Port: uint16(port), Timestamp: stamp, Protocol: "tcp", State: "open"})
	})
	if err != nil {
		return err
	}
	if !header {
		return fmt.Errorf("missing ZMap result header")
	}
	return nil
}

func (d *decoder) Status(line string) (scan.Progress, bool, error) {
	unknown := scan.Progress{PercentUnknown: true, RateUnknown: true, ETAUnknown: true}
	if strings.HasPrefix(line, "real-time,") {
		if line != StatusHeader || d.statusHeader {
			return unknown, false, fmt.Errorf("unrecognized ZMap status header")
		}
		d.statusHeader = true
		return unknown, false, nil
	}
	// Native diagnostics start with a month name, not an ISO CSV date.
	if len(line) < 20 || line[4] != '-' || line[7] != '-' || line[10] != ' ' || !strings.Contains(line, ",") {
		return unknown, false, nil
	}
	if !d.statusHeader {
		return unknown, false, fmt.Errorf("ZMap status before schema header")
	}
	row, err := csv.NewReader(strings.NewReader(line)).Read()
	if err != nil || len(row) != 22 || row[19] != "" {
		return unknown, false, fmt.Errorf("malformed ZMap status record")
	}
	if _, err := time.Parse("2006-01-02 15:04:05", row[0]); err != nil {
		return unknown, false, fmt.Errorf("invalid ZMap status time")
	}
	remaining, err := finite(row[2])
	if err != nil || remaining > float64(math.MaxInt64/int64(time.Second)) {
		return unknown, false, fmt.Errorf("invalid ZMap remaining time")
	}
	percent, err := finite(row[3])
	if err != nil || percent > 100 {
		return unknown, false, fmt.Errorf("invalid ZMap completion estimate")
	}
	senders, err := strconv.ParseUint(row[5], 10, 8)
	if err != nil || senders > 1 {
		return unknown, false, fmt.Errorf("invalid ZMap active sender count")
	}
	if _, err := strconv.ParseUint(row[6], 10, 64); err != nil {
		return unknown, false, fmt.Errorf("invalid ZMap sent count")
	}
	rate, err := finite(row[7])
	if err != nil {
		return unknown, false, fmt.Errorf("invalid ZMap send rate")
	}
	failures, err := strconv.ParseUint(row[18], 10, 64)
	if err != nil || failures != 0 {
		return unknown, false, fmt.Errorf("ZMap reports invalid or failed sends: %s", row[18])
	}
	p := scan.Progress{PercentUnknown: true, Rate: rate, ETA: time.Duration(remaining) * time.Second, Waiting: senders == 0}
	if p.Waiting {
		p.Percent, p.PercentUnknown, p.Rate = 100, false, 0
	}
	return p, true, nil
}
func finite(text string) (float64, error) {
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0, fmt.Errorf("invalid nonnegative finite value")
	}
	return n, nil
}
