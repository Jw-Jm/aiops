package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type RunnerCallbackError struct{ Status int }

func (e *RunnerCallbackError) Error() string { return "runner callback unavailable or rejected" }

type RunnerClient struct {
	Client                *http.Client
	Endpoint, Token       string
	TenantID, ExecutionID uuid.UUID
}

func (c RunnerClient) call(ctx context.Context, kind string, input, output any) error {
	if c.Client == nil || len(c.Token) != 43 || c.TenantID == uuid.Nil || c.ExecutionID == uuid.Nil {
		return ErrInvalid
	}
	path := "/internal/v1/tenants/" + c.TenantID.String() + "/command-executions/" + c.ExecutionID.String() + "/" + kind
	req, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint+path, bytes.NewReader(Canonical(input)))
	if err != nil {
		return ErrInvalid
	}
	req.Header.Set("X-Runner-Token", c.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Client.Do(req)
	if err != nil {
		return &RunnerCallbackError{}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return &RunnerCallbackError{Status: res.StatusCode}
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(output)
	}
	return nil
}

type runnerOutput struct {
	mu                sync.Mutex
	client            RunnerClient
	ctx               context.Context
	seq, total, limit int64
	truncated         bool
	err               error
}
type runnerStream struct {
	output *runnerOutput
	name   string
}

func (w runnerStream) Write(b []byte) (int, error) {
	o := w.output
	o.mu.Lock()
	defer o.mu.Unlock()
	original := len(b)
	if o.err != nil {
		return original, nil
	}
	for len(b) > 0 {
		if o.total >= o.limit {
			o.truncated = true
			return original, nil
		}
		n := min(len(b), MaxChunkBytes, int(o.limit-o.total))
		chunk := OutputChunk{ExecutionID: o.client.ExecutionID, Stream: w.name, Seq: o.seq + 1, Bytes: b[:n]}
		if err := o.client.call(o.ctx, "output", chunk, nil); err != nil {
			o.err = err
			return original, nil
		}
		o.seq++
		o.total += int64(n)
		b = b[n:]
	}
	return original, nil
}
func (c RunnerClient) Run(ctx context.Context) error {
	// Generate SSH identity before claim; only its public half leaves the Runner.
	private, public, err := newSSHKey()
	if err != nil {
		return err
	}
	defer clear(private)
	var claim ClaimedCommand
	if err = c.call(ctx, "claim", map[string]string{"publicKey": public}, &claim); err != nil {
		return err
	}
	if claim.ExecutionID != c.ExecutionID || ValidateCommand(claim.Command, "bash") != nil || claim.TimeoutSeconds < 1 || claim.TimeoutSeconds > 900 || claim.MaxOutputBytes < 1 || claim.MaxOutputBytes > DefaultMaxOutputBytes {
		return ErrInvalid
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(claim.TimeoutSeconds)*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("/run/ops", "execution-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	environment := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir, "LANG=C.UTF-8"}
	var cmd *exec.Cmd
	if cred := claim.Credentials.Kubernetes; cred != nil {
		config := Kubeconfig(*cred)
		path := filepath.Join(dir, "kubeconfig")
		if err = os.WriteFile(path, config, 0600); err != nil {
			clear(config)
			return err
		}
		clear(config)
		environment = append(environment, "KUBECONFIG="+path)
		cmd = exec.CommandContext(runCtx, "/bin/bash", "--noprofile", "--norc", "-o", "pipefail", "-c", claim.Command)
	} else if cred := claim.Credentials.SSH; cred != nil {
		cmd, err = AnsibleCommand(runCtx, dir, private, *cred, claim.Command)
		if err != nil {
			return err
		}
	} else {
		return ErrDenied
	}
	cmd.Env = environment
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.WaitDelay = 2 * time.Second
	configureProcessGroup(cmd)
	output := &runnerOutput{client: c, ctx: ctx, limit: claim.MaxOutputBytes}
	cmd.Stdout = runnerStream{output, "stdout"}
	cmd.Stderr = runnerStream{output, "stderr"}
	err = cmd.Run()
	// Killing the local transport does not prove a remote process stopped.
	// Without a durable result callback the Worker records execution_unknown.
	if runCtx.Err() != nil {
		return errors.New("runner execution interrupted; outcome uncertain")
	}
	exit := 0
	if err != nil {
		exit = 255
		var status *exec.ExitError
		if errors.As(err, &status) && status.ExitCode() >= 0 {
			exit = status.ExitCode()
		}
	}
	if claim.Credentials.SSH != nil {
		var result struct {
			Certain  bool `json:"certain"`
			ExitCode int  `json:"exitCode"`
		}
		raw, readErr := os.ReadFile(filepath.Join(dir, "transport-result.json"))
		if readErr != nil || len(raw) > 1024 || json.Unmarshal(raw, &result) != nil || !result.Certain || result.ExitCode < 0 || result.ExitCode > 255 {
			return errors.New("SSH result not proven")
		}
		exit = result.ExitCode
	}
	if output.err != nil {
		return output.err
	} // Lost output/callback remains uncertain for reconciliation.
	return c.call(ctx, "finish", map[string]any{"exitCode": exit, "finalSeq": output.seq, "truncated": output.truncated}, nil)
}
