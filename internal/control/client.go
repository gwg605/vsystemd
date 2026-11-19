package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/transport"
)

func (c *Controller) request(msgType string, payload any) (transport.Message, error) {
	local := transport.NewPeer(transport.PeerControl, fmt.Sprintf("systemctl_%d", os.Getpid()), false)
	tr, err := c.transport(local, nil)
	if err != nil {
		return transport.Message{}, fmt.Errorf("create transport: %w", err)
	}
	defer tr.Close()

	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return transport.Message{}, err
		}
		raw = data
	}

	slog.Debug("systemctl: sending request", "type", msgType, "local", local.String())
	response, err := tr.Req(tr.Target(transport.PeerDaemon), transport.Message{Type: msgType, Payload: raw})
	if err != nil {
		slog.Error("systemctl: request failed", "type", msgType, "error", err)
		return transport.Message{}, fmt.Errorf("daemon is not available: %w", err)
	}
	slog.Debug("systemctl: response received", "type", response.Type, "request_id", response.RequestID)
	return response, nil
}

func responseError(response transport.Message) error {
	if response.Type != protocol.TypeErrorResponse {
		return nil
	}
	var failure protocol.ErrorResponse
	if err := json.Unmarshal(response.Payload, &failure); err != nil {
		return err
	}
	return errors.New(failure.Error)
}
