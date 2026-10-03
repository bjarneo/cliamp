package main

// ipc_client.go holds the V2 IPC client of the cliamp CLI. The subcommands,
// cliamp open and cliamp remote send their requests through it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/bjarneo/cliamp/ipc"
)

// cliRequestID is the V2 request ID of every CLI request.
const cliRequestID = `"cliamp"`

const (
	// ipcWait bounds how long a CLI command waits for the result of its job.
	// A job that never finishes then cannot hang a script, such as a Waybar
	// module that runs cliamp next.
	ipcWait = 30 * time.Second
	// ipcLoadWait bounds operations that load or resolve a whole list, as a
	// yt-dlp playlist can take minutes to resolve.
	ipcLoadWait = 5 * time.Minute
)

// userIPCError renders ipc.ErrNotRunning as the wording users see. The ipc
// package returns a bare sentinel, so all CLI copy stays in the command layer.
func userIPCError(err error) error {
	if errors.Is(err, ipc.ErrNotRunning) {
		return fmt.Errorf("cliamp is not running (no socket at %s)", ipc.DefaultSocketPath())
	}
	return err
}

// sendV2 sends one request with the CLI request ID to the running cliamp.
func sendV2(request ipc.V2Request) (ipc.V2Response, error) {
	request.ID = json.RawMessage(cliRequestID)
	response, err := ipc.SendV2(ipc.DefaultSocketPath(), request)
	if err != nil {
		return ipc.V2Response{}, userIPCError(err)
	}
	return response, nil
}

// ipcSend submits operation and waits up to ipcWait for its result.
func ipcSend(operation string, params ipc.Request) (ipc.Response, error) {
	return ipcSendWithin(operation, params, ipcWait)
}

// ipcSendWithin submits operation and waits up to timeout for its result.
// The job keeps running in cliamp after the timeout. Plugin commands can
// run for minutes, for example for yt-dlp downloads.
func ipcSendWithin(operation string, params ipc.Request, timeout time.Duration) (ipc.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	response, err := ipcSendWithContext(ctx, operation, params)
	if errors.Is(err, context.DeadlineExceeded) {
		return response, fmt.Errorf("%s did not finish within %s", operation, timeout)
	}
	return response, err
}

func ipcSendWithContext(ctx context.Context, operation string, params ipc.Request) (ipc.Response, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return ipc.Response{}, fmt.Errorf("marshal %s parameters: %w", operation, err)
	}
	response, err := sendV2(ipc.V2Request{
		Method:    "operation.submit",
		Operation: operation,
		Params:    raw,
	})
	if err != nil {
		return ipc.Response{}, err
	}
	if err := v2ResponseError(response); err != nil {
		return ipc.Response{}, err
	}
	if response.Job == nil {
		return ipc.Response{}, fmt.Errorf("%s returned no job", operation)
	}
	response, err = waitForV2Job(ctx, response.Job.ID)
	if err != nil {
		return ipc.Response{}, err
	}
	if response.Job == nil {
		return ipc.Response{}, fmt.Errorf("%s completed without a job", operation)
	}
	var result ipc.Response
	if err := json.Unmarshal(response.Job.Result, &result); err != nil {
		return ipc.Response{}, fmt.Errorf("decode %s result: %w", operation, err)
	}
	if !result.OK {
		return result, fmt.Errorf("%s", result.Error)
	}
	return result, nil
}

// waitForV2Job polls the job until it ends or ctx is done.
func waitForV2Job(ctx context.Context, jobID string) (ipc.V2Response, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := sendV2(ipc.V2Request{Method: "job.get", JobID: jobID})
		if err != nil {
			return ipc.V2Response{}, err
		}
		if err := v2ResponseError(response); err != nil {
			return ipc.V2Response{}, err
		}
		if response.Job != nil {
			switch response.Job.State {
			case ipc.JobSucceeded:
				return response, nil
			case ipc.JobFailed, ipc.JobCanceled:
				if response.Job.Error != nil {
					if response.Job.Error.Detail != "" {
						return ipc.V2Response{}, fmt.Errorf("job %s (%s): %s (%s)", response.Job.State, response.Job.Error.Code, response.Job.Error.Message, response.Job.Error.Detail)
					}
					return ipc.V2Response{}, fmt.Errorf("job %s (%s): %s", response.Job.State, response.Job.Error.Code, response.Job.Error.Message)
				}
				return ipc.V2Response{}, fmt.Errorf("job %s", response.Job.State)
			}
		}
		select {
		case <-ctx.Done():
			return ipc.V2Response{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func ipcState() (ipc.RuntimeSnapshot, error) {
	response, err := sendV2(ipc.V2Request{Method: "state.get"})
	if err != nil {
		return ipc.RuntimeSnapshot{}, err
	}
	if err := v2ResponseError(response); err != nil {
		return ipc.RuntimeSnapshot{}, err
	}
	if response.Snapshot == nil {
		return ipc.RuntimeSnapshot{}, fmt.Errorf("state response has no snapshot")
	}
	return *response.Snapshot, nil
}

// stateResult converts a snapshot to the Response shape that cliamp status
// --json prints.
func stateResult(snapshot ipc.RuntimeSnapshot) ipc.Response {
	return ipc.Response{
		OK:         true,
		State:      snapshot.State,
		Track:      snapshot.Track,
		Position:   snapshot.Position,
		Duration:   snapshot.Duration,
		Volume:     snapshot.Volume,
		Playlist:   snapshot.Playlist,
		Index:      snapshot.Index,
		Total:      snapshot.Total,
		Visualizer: snapshot.Visualizer,
		Shuffle:    snapshot.Shuffle,
		Repeat:     snapshot.Repeat,
		Mono:       snapshot.Mono,
		Speed:      snapshot.Speed,
		EQPreset:   snapshot.EQPreset,
		Theme:      snapshot.Theme,
		EQBands:    snapshot.EQBands,
	}
}

// statusJSON is what cliamp status --json prints. It always holds the
// position, the volume and the index, because 0 is a real value of each.
// Its fields hide the omitempty fields of the same names in Response.
type statusJSON struct {
	ipc.Response
	Position float64 `json:"position"`
	Volume   float64 `json:"volume"`
	Index    int     `json:"index"`
}

func newStatusJSON(resp ipc.Response) statusJSON {
	return statusJSON{Response: resp, Position: resp.Position, Volume: resp.Volume, Index: resp.Index}
}

func printV2Response(response ipc.V2Response) error {
	if err := v2ResponseError(response); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(response)
}

func v2ResponseError(response ipc.V2Response) error {
	if response.OK {
		return nil
	}
	if response.Error == nil {
		return fmt.Errorf("remote operation failed")
	}
	if response.Error.Detail != "" {
		return fmt.Errorf("remote operation failed (%s): %s (%s)", response.Error.Code, response.Error.Message, response.Error.Detail)
	}
	return fmt.Errorf("remote operation failed (%s): %s", response.Error.Code, response.Error.Message)
}
