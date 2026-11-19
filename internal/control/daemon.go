package control

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gwg605/vsystemd/internal/protocol"
	"github.com/gwg605/vsystemd/internal/unit"
)

func (c *Controller) TimerEnable(id unit.ID, enabled bool) error {
	return c.timerRequest(protocol.TypeTimerEnable, protocol.TimerRequest{Unit: id.String(), Enabled: enabled})
}

func (c *Controller) TimerStart(id unit.ID) error {
	return c.timerRequest(protocol.TypeTimerStart, protocol.TimerRequest{Unit: id.String()})
}

func (c *Controller) TimerStop(id unit.ID) error {
	return c.timerRequest(protocol.TypeTimerStop, protocol.TimerRequest{Unit: id.String()})
}

func (c *Controller) Status(id unit.ID) (protocol.StatusResponse, error) {
	response, err := c.request(protocol.TypeStatusRequest, protocol.StatusRequest{Unit: id.String()})
	if err != nil {
		return protocol.StatusResponse{}, err
	}
	if err := responseError(response); err != nil {
		return protocol.StatusResponse{}, err
	}

	var status protocol.StatusResponse
	if err := json.Unmarshal(response.Payload, &status); err != nil {
		return protocol.StatusResponse{}, err
	}
	return status, nil
}

func (c *Controller) ListUnits() (protocol.ListUnitsResponse, error) {
	response, err := c.request(protocol.TypeListUnits, nil)
	if err != nil {
		return protocol.ListUnitsResponse{}, err
	}
	if err := responseError(response); err != nil {
		return protocol.ListUnitsResponse{}, err
	}

	var list protocol.ListUnitsResponse
	if err := json.Unmarshal(response.Payload, &list); err != nil {
		return protocol.ListUnitsResponse{}, err
	}
	return list, nil
}

func (c *Controller) DaemonReload() error {
	response, err := c.request(protocol.TypeDaemonReload, nil)
	if err != nil {
		return err
	}
	if err := responseError(response); err != nil {
		return err
	}

	var result protocol.ReloadResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		return err
	}
	if !result.Success {
		return errors.New("daemon reload failed")
	}
	return nil
}

func (c *Controller) timerRequest(msgType string, request protocol.TimerRequest) error {
	response, err := c.request(msgType, request)
	if err != nil {
		return err
	}
	if err := responseError(response); err != nil {
		return err
	}

	var result protocol.TimerResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("%s failed", request.Unit)
	}
	return nil
}
