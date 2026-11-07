package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"

	"github.com/HiAriesZhou/litecontext/internal/clientconfig"
	"github.com/HiAriesZhou/litecontext/internal/strictjson"
)

func runClientConfig(args []string) error {
	flags := flag.NewFlagSet("client-config", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	state := flags.String("state-dir", "", "device-local undo directory (default: user configuration directory)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	response := struct {
		Version int                `json:"version"`
		OK      bool               `json:"ok"`
		Result  *clientconfig.Plan `json:"result,omitempty"`
		Error   string             `json:"error,omitempty"`
	}{Version: 1}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, (64<<10)+1))
	var request clientconfig.Request
	if err != nil || len(data) > 64<<10 || flags.NArg() != 0 || strictjson.Decode(data, &request) != nil || request.Version != 1 {
		response.Error = "unsupported or invalid configuration"
	} else {
		if *state == "" {
			*state, err = clientconfig.DefaultStateDir()
		}
		var plan clientconfig.Plan
		if err == nil {
			switch request.Op {
			case "preview":
				plan, err = clientconfig.Preview(request, *state)
			case "apply":
				plan, err = clientconfig.Apply(context.Background(), request, *state)
			case "restore":
				plan, err = clientconfig.Restore(context.Background(), request, *state)
			default:
				err = clientconfig.ErrInvalid
			}
		}
		if err != nil {
			switch err {
			case clientconfig.ErrConflict, clientconfig.ErrManaged, clientconfig.ErrInvalid:
				response.Error = err.Error()
			default:
				response.Error = "configuration operation failed; preview before retrying"
			}
		} else {
			response.OK = true
			response.Result = &plan
		}
	}
	return json.NewEncoder(os.Stdout).Encode(response)
}
