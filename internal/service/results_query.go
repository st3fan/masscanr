package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type ResultFilter struct {
	RunID    string
	TargetID string
	IP       netip.Addr
	Port     *uint16
	After    string
	Sort     string
	Limit    int
}

func resultID(runID string, ip netip.Addr, port uint16) string {
	return base64.RawURLEncoding.EncodeToString([]byte(runID + "|" + ip.String() + "|" + strconv.Itoa(int(port))))
}

func parseResultID(id string) (string, netip.Addr, uint16, error) {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return "", netip.Addr{}, 0, fmt.Errorf("invalid result cursor")
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 || parts[0] == "" {
		return "", netip.Addr{}, 0, fmt.Errorf("invalid result cursor")
	}
	ip, err := netip.ParseAddr(parts[1])
	if err != nil || !ip.Is4() {
		return "", netip.Addr{}, 0, fmt.Errorf("invalid result cursor")
	}
	port, err := strconv.ParseUint(parts[2], 10, 16)
	if err != nil {
		return "", netip.Addr{}, 0, fmt.Errorf("invalid result cursor")
	}
	return parts[0], ip, uint16(port), nil
}

func (s *Store) ListScanResults(ctx context.Context, filter ResultFilter) ([]ScanResult, error) {
	if filter.Limit < 1 || filter.Limit > 1000 {
		return nil, fmt.Errorf("limit must be 1..1000")
	}
	if filter.IP.IsValid() && !filter.IP.Is4() {
		return nil, fmt.Errorf("filter IP must be IPv4")
	}
	if filter.Sort != "" && filter.Sort != "ip" && filter.Sort != "-ip" && filter.Sort != "port" && filter.Sort != "-port" {
		return nil, fmt.Errorf("sort must be ip, -ip, port, or -port")
	}
	query := `SELECT r.run_id,r.target_id,host(r.ip),r.port,r.protocol,r.first_seen::text,r.last_seen::text,r.observations::text,r.selector,r.origin_prefix,run.result_state
FROM masscanr.scan_results r JOIN masscanr.runs run ON run.id=r.run_id WHERE true`
	args := []any{}
	add := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if filter.RunID != "" {
		query += ` AND r.run_id=` + add(filter.RunID)
	}
	if filter.TargetID != "" {
		query += ` AND r.target_id=` + add(filter.TargetID)
	}
	if filter.IP.IsValid() {
		query += ` AND r.ip=` + add(filter.IP.String()) + `::inet`
	}
	if filter.Port != nil {
		query += ` AND r.port=` + add(int(*filter.Port))
	}
	order := `r.run_id,r.ip,r.port`
	compare := `>`
	cursorColumns := `r.run_id,r.ip,r.port`
	if filter.Sort == "ip" || filter.Sort == "-ip" {
		order = `r.ip,r.port,r.run_id`
		cursorColumns = `r.ip,r.port,r.run_id`
	}
	if filter.Sort == "port" || filter.Sort == "-port" {
		order = `r.port,r.ip,r.run_id`
		cursorColumns = `r.port,r.ip,r.run_id`
	}
	if strings.HasPrefix(filter.Sort, "-") {
		compare = `<`
	}
	if filter.After != "" {
		runID, ip, port, err := parseResultID(filter.After)
		if err != nil {
			return nil, err
		}
		cursorValues := ""
		switch filter.Sort {
		case "ip", "-ip":
			cursorValues = add(ip.String()) + `::inet,` + add(int(port)) + `,` + add(runID)
		case "port", "-port":
			cursorValues = add(int(port)) + `,` + add(ip.String()) + `::inet,` + add(runID)
		default:
			cursorValues = add(runID) + `,` + add(ip.String()) + `::inet,` + add(int(port))
		}
		query += ` AND (` + cursorColumns + `)` + compare + `(` + cursorValues + `)`
	}
	if strings.HasPrefix(filter.Sort, "-") {
		query += ` ORDER BY ` + strings.ReplaceAll(order, ",", ` DESC,`) + ` DESC LIMIT ` + add(filter.Limit)
	} else {
		query += ` ORDER BY ` + order + ` LIMIT ` + add(filter.Limit)
	}
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScanResult{}
	for rows.Next() {
		var result ScanResult
		var ipText, first, last, count string
		var port int
		if err = rows.Scan(&result.RunID, &result.TargetID, &ipText, &port, &result.Protocol, &first, &last, &count, &result.Selector, &result.OriginPrefix, &result.Coverage); err != nil {
			return nil, err
		}
		result.IP, err = netip.ParseAddr(ipText)
		if err != nil {
			return nil, err
		}
		result.Port = uint16(port)
		if result.FirstSeen, err = strconv.ParseUint(first, 10, 64); err != nil {
			return nil, err
		}
		if result.LastSeen, err = strconv.ParseUint(last, 10, 64); err != nil {
			return nil, err
		}
		if result.Observations, err = strconv.ParseUint(count, 10, 64); err != nil {
			return nil, err
		}
		result.ID = resultID(result.RunID, result.IP, result.Port)
		out = append(out, result)
	}
	return out, rows.Err()
}

type ResultCoverage struct {
	State      string `json:"state"`
	Checkpoint uint64 `json:"journal_checkpoint"`
	RunPhase   string `json:"run_phase"`
}

func (s *Store) GetResultCoverage(ctx context.Context, runID string) (ResultCoverage, error) {
	var coverage ResultCoverage
	var checkpoint int64
	err := s.Pool.QueryRow(ctx, `SELECT result_state,result_checkpoint,data->>'phase' FROM masscanr.runs WHERE id=$1`, runID).Scan(&coverage.State, &checkpoint, &coverage.RunPhase)
	if err == pgx.ErrNoRows {
		return coverage, ErrNotFound
	}
	if err != nil {
		return coverage, err
	}
	coverage.Checkpoint = uint64(checkpoint)
	return coverage, nil
}
