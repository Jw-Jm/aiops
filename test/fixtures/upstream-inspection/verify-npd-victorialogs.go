package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ruleFile struct {
	Rules []struct {
		Reason  string `json:"reason"`
		Pattern string `json:"pattern"`
	} `json:"rules"`
}

type vectorFile struct {
	Cases []struct {
		ID            string `json:"id"`
		Reason        string `json:"reason"`
		Pattern       string `json:"pattern"`
		Message       string `json:"message"`
		ExpectedMatch bool   `json:"expectedMatch"`
	} `json:"cases"`
}

func main() {
	binary := flag.String("victoria-logs", "", "path to the pinned VictoriaLogs binary")
	rulesPath := flag.String("rules", "", "path to the pinned NPD kernel-monitor.json")
	casesPath := flag.String("cases", "", "path to NPD/VictoriaLogs conformance vectors")
	flag.Parse()
	if *binary == "" || *rulesPath == "" || *casesPath == "" {
		fatal(errors.New("-victoria-logs, -rules, and -cases are required"))
	}

	var rules ruleFile
	readJSON(*rulesPath, &rules)
	var vectors vectorFile
	readJSON(*casesPath, &vectors)
	if len(rules.Rules) == 0 || len(vectors.Cases) == 0 {
		fatal(errors.New("NPD rules or conformance cases are empty"))
	}
	ruleSet := make(map[string]bool, len(rules.Rules))
	for _, rule := range rules.Rules {
		ruleSet[rule.Reason+"\x00"+rule.Pattern] = true
	}
	for _, test := range vectors.Cases {
		if !ruleSet[test.Reason+"\x00"+test.Pattern] {
			fatal(fmt.Errorf("case %s does not match a locked NPD rule", test.ID))
		}
		matcher, err := regexp.Compile(test.Pattern + `\z`)
		if err != nil {
			fatal(fmt.Errorf("compile NPD pattern for %s: %w", test.ID, err))
		}
		if got := matcher.MatchString(test.Message); got != test.ExpectedMatch {
			fatal(fmt.Errorf("NPD matcher %s=%v, want %v", test.ID, got, test.ExpectedMatch))
		}
	}

	dataDir, err := os.MkdirTemp("", "task29-vlogs-")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(dataDir)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, *binary, "-storageDataPath="+filepath.Join(dataDir, "storage"), "-httpListenAddr=127.0.0.1:9428", "-retentionPeriod=1d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fatal(fmt.Errorf("start isolated VictoriaLogs: %w", err))
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	const base = "http://127.0.0.1:9428"
	client := &http.Client{Timeout: 10 * time.Second}
	waitReady(ctx, client, base)

	var ndjson bytes.Buffer
	for _, test := range vectors.Cases {
		line, err := json.Marshal(map[string]string{"source": "task29-npd-fixture", "case_id": test.ID, "message": test.Message, "timestamp": "0"})
		if err != nil {
			fatal(err)
		}
		ndjson.Write(line)
		ndjson.WriteByte('\n')
	}
	ingestURL := base + "/insert/jsonline?_stream_fields=source,case_id&_msg_field=message&_time_field=timestamp"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ingestURL, bytes.NewReader(ndjson.Bytes()))
	if err != nil {
		fatal(err)
	}
	req.Header.Set("Content-Type", "application/stream+json")
	resp, err := client.Do(req)
	if err != nil {
		fatal(fmt.Errorf("ingest NPD fixture: %w", err))
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		fatal(fmt.Errorf("ingest status %s: %s", resp.Status, body))
	}
	// Wait for the write path to make the fixture rows queryable before running
	// the filter comparison. VictoriaLogs may flush in the background.
	waitForRows(ctx, client, base, len(vectors.Cases))

	for _, test := range vectors.Cases {
		logsQL := fmt.Sprintf(`{source="task29-npd-fixture",case_id=%s} AND _msg:~%s`, strconv.Quote(test.ID), strconv.Quote(test.Pattern+`\z`))
		form := url.Values{"query": []string{logsQL}, "limit": []string{"2"}}
		queryURL := base + "/select/logsql/query"
		resp, err := client.PostForm(queryURL, form)
		if err != nil {
			fatal(fmt.Errorf("query VictoriaLogs case %s: %w", test.ID, err))
		}
		queryBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			fatal(fmt.Errorf("query VictoriaLogs case %s status %s: %s; query=%s", test.ID, resp.Status, queryBody, logsQL))
		}
		got := 0
		for _, line := range strings.Split(strings.TrimSpace(string(queryBody)), "\n") {
			if line != "" {
				got++
			}
		}
		want := 0
		if test.ExpectedMatch {
			want = 1
		}
		if got != want {
			fatal(fmt.Errorf("VictoriaLogs LogsQL case %s matched %d entries, want %d; query=%s; body=%s", test.ID, got, want, logsQL, queryBody))
		}
		fmt.Printf("case=%s npd=%v victorialogs=%d\n", test.ID, test.ExpectedMatch, got)
	}
	fmt.Printf("verified %d NPD rule/message cases against VictoriaLogs LogsQL\n", len(vectors.Cases))
}

func waitForRows(ctx context.Context, client *http.Client, base string, want int) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			fatal(fmt.Errorf("waiting for queryable VictoriaLogs fixture rows: %w", ctx.Err()))
		case <-deadline.C:
			fatal(fmt.Errorf("VictoriaLogs did not expose all fixture rows within 10s (want %d)", want))
		case <-ticker.C:
			form := url.Values{"query": []string{"*"}, "limit": []string{strconv.Itoa(want + 1)}}
			resp, err := client.PostForm(base+"/select/logsql/query", form)
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				continue
			}
			count := 0
			for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
				if line != "" {
					count++
				}
			}
			if count >= want {
				return
			}
		}
	}
}

func readJSON(path string, target any) {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		fatal(err)
	}
}

func waitReady(ctx context.Context, client *http.Client, base string) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fatal(fmt.Errorf("VictoriaLogs did not become ready: %w", ctx.Err()))
		case <-ticker.C:
			resp, err := client.Get(base + "/health")
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode/100 == 2 {
					return
				}
			}
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "FAIL:", err)
	os.Exit(1)
}
