package rdp

import (
	"fmt"
	"io"

	"rustxterm-go/internal/rdpmit/protocol/pdu"
)

func (c *Client) connectionFinalization() error {
	var err error

	synchronize := pdu.NewSynchronize(c.shareID, c.userID)
	if err = c.mcsLayer.Send(c.userID, c.channelIDMap["global"], synchronize.Serialize()); err != nil {
		return err
	}

	controlCooperate := pdu.NewControl(c.shareID, c.userID, pdu.ControlActionCooperate)
	if err = c.mcsLayer.Send(c.userID, c.channelIDMap["global"], controlCooperate.Serialize()); err != nil {
		return err
	}

	controlRequestControl := pdu.NewControl(c.shareID, c.userID, pdu.ControlActionRequestControl)
	if err = c.mcsLayer.Send(c.userID, c.channelIDMap["global"], controlRequestControl.Serialize()); err != nil {
		return err
	}

	fontList := pdu.NewFontList(c.shareID, c.userID)

	err = c.mcsLayer.Send(c.userID, c.channelIDMap["global"], fontList.Serialize())
	if err != nil {
		return err
	}

	var (
		serverSynchronizeReceived bool
		controlCooperateReceived  bool
		grantedControlReceived    bool
		fontMapReceived           bool

		dataPDU *pdu.Data
		wire    io.Reader
	)

	for !serverSynchronizeReceived || !controlCooperateReceived || !grantedControlReceived || !fontMapReceived {
		var channelID uint16
		channelID, wire, err = c.mcsLayer.Receive()
		if err != nil {
			return err
		}
		if handled, derr := c.dispatchChannel(channelID, wire); handled { // rustxterm
			if derr != nil {
				return derr
			}
			continue
		}

		dataPDU = &pdu.Data{}
		if err = dataPDU.Deserialize(wire); err != nil {
			return err
		}

		pduType2 := dataPDU.ShareDataHeader.PDUType2

		switch {
		case pduType2.IsSynchronize():
			serverSynchronizeReceived = true
		case pduType2.IsControl():
			if dataPDU.ControlPDUData.Action == pdu.ControlActionCooperate {
				controlCooperateReceived = true
			}

			if dataPDU.ControlPDUData.Action == pdu.ControlActionGrantedControl {
				grantedControlReceived = true
			}
		case pduType2.IsFontmap():
			fontMapReceived = true
		case pduType2.IsErrorInfo():
			return fmt.Errorf("server error info: %d", dataPDU.ErrorInfoPDUData.ErrorInfo)
		default:
			return fmt.Errorf("unknown server message with pduType2 = %d", pduType2)
		}
	}

	return nil
}
