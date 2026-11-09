package fastpath

import (
	"encoding/binary"
	"errors"
	"io"
)

type UpdateCode uint8

const (
	UpdateCodeOrders       UpdateCode = 0x0
	UpdateCodeBitmap       UpdateCode = 0x1
	UpdateCodePalette      UpdateCode = 0x2
	UpdateCodeSynchronize  UpdateCode = 0x3
	UpdateCodeSurfCMDs     UpdateCode = 0x4
	UpdateCodePTRNull      UpdateCode = 0x5
	UpdateCodePTRDefault   UpdateCode = 0x6
	UpdateCodePTRPosition  UpdateCode = 0x8
	UpdateCodeColor        UpdateCode = 0x9
	UpdateCodeCached       UpdateCode = 0xa
	UpdateCodePointer      UpdateCode = 0xb
	UpdateCodeLargePointer UpdateCode = 0xc
)

type Fragment uint8

const (
	FragmentSingle Fragment = 0x0
	FragmentLast   Fragment = 0x1
	FragmentFirst  Fragment = 0x2
	FragmentNext   Fragment = 0x3
)

type Compression uint8

const (
	CompressionUsed Compression = 0x2
)

type Update struct {
	UpdateCode       UpdateCode
	fragmentation    Fragment
	compression      Compression
	compressionFlags uint8
	size             uint16
}

func (u *Update) Deserialize(wire io.Reader) error {
	var err error

	var updateHeader uint8
	err = binary.Read(wire, binary.LittleEndian, &updateHeader)
	if err != nil {
		return err
	}

	u.UpdateCode = UpdateCode(updateHeader & 0xf)
	u.fragmentation = Fragment((updateHeader >> 4) & 0x3)
	u.compression = Compression((updateHeader >> 6) & 0x3)

	if u.compression&CompressionUsed == CompressionUsed {
		err = binary.Read(wire, binary.LittleEndian, &u.compressionFlags)
		if err != nil {
			return err
		}
	}

	err = binary.Read(wire, binary.LittleEndian, &u.size)
	if err != nil {
		return err
	}

	//switch u.UpdateCode {
	//case UpdateCodePalette:
	//	u.paletteUpdateData = &paletteUpdateData{}
	//
	//	return u.paletteUpdateData.Deserialize(wire)
	//case UpdateCodeBitmap:
	//	u.bitmapUpdateData = &bitmapUpdateData{}
	//
	//	return u.bitmapUpdateData.Deserialize(wire)
	//case UpdateCodeSynchronize: // do nothing
	//case UpdateCodePTRPosition:
	//	u.pointerPositionUpdateData = &pointerPositionUpdateData{}
	//
	//	return u.pointerPositionUpdateData.Deserialize(wire)
	//case UpdateCodePTRNull: // do nothing
	//case UpdateCodePTRDefault: // do nothing
	//case UpdateCodeCached:
	//	u.colorPointerUpdateData = &colorPointerUpdateData{}
	//
	//	return u.colorPointerUpdateData.Deserialize(wire)
	//}

	d := make([]byte, u.size)
	_, err = io.ReadFull(wire, d)
	if err != nil {
		return err
	}

	return nil
}

type UpdatePDUAction uint8

const (
	UpdatePDUActionFastPath UpdatePDUAction = 0x0
	UpdatePDUActionX224     UpdatePDUAction = 0x3
)

type UpdatePDUFlag uint8

const (
	UpdatePDUFlagSecureChecksum UpdatePDUFlag = 0x1
	UpdatePDUFlagEncrypted      UpdatePDUFlag = 0x2
)

type UpdatePDU struct {
	fpOutputHeader uint8
	Action         UpdatePDUAction
	Flags          UpdatePDUFlag
	Data           []byte
}

var ErrUnexpectedX224 = errors.New("unexpected x224")

func (pdu *UpdatePDU) Deserialize(wire io.Reader) error {
	err := binary.Read(wire, binary.LittleEndian, &pdu.fpOutputHeader)
	if err != nil {
		return err
	}

	pdu.Action = UpdatePDUAction(pdu.fpOutputHeader & 0x3)
	pdu.Flags = UpdatePDUFlag((pdu.fpOutputHeader >> 6) & 0x3)

	if pdu.Action == UpdatePDUActionX224 {
		return ErrUnexpectedX224
	}

	var (
		length           uint16
		length1, length2 uint8
	)

	err = binary.Read(wire, binary.LittleEndian, &length1)
	if err != nil {
		return err
	}

	length = uint16(length1)
	headerLen := uint16(2) // fpOutputHeader + length1

	if length1&0x80 == 0x80 {
		err = binary.Read(wire, binary.LittleEndian, &length2)
		if err != nil {
			return err
		}

		length1 -= 0x80

		length = binary.BigEndian.Uint16([]byte{length1, length2})
		headerLen = 3
	}

	if length > 0x7FFF {
		return errors.New("too big packet")
	}

	// rustxterm: the length field counts the whole PDU (MS-RDPBCGR 2.2.9.1.2), header included;
	// reading that many bytes after the header ran into the next PDU.
	if length < headerLen {
		return errors.New("fast-path PDU shorter than its header")
	}
	length -= headerLen

	if len(pdu.Data) != 0 {
		pdu.Data = pdu.Data[:length]
	} else {
		pdu.Data = make([]byte, length)
	}

	_, err = io.ReadFull(wire, pdu.Data)
	if err != nil {
		return err
	}

	return nil
}

func (i *Protocol) Receive() (*UpdatePDU, error) {
	var pdu UpdatePDU
	pdu.Data = i.updatePDUData
	if err := pdu.Deserialize(i.conn); err != nil {
		return nil, err
	}

	// rustxterm: under standard RDP security the client's Decrypt opens (or refuses) every PDU.
	encrypted := pdu.Flags&UpdatePDUFlagEncrypted == UpdatePDUFlagEncrypted
	if encrypted && i.Decrypt == nil {
		return nil, errors.New("encryption not supported")
	}
	if i.Decrypt != nil {
		data, err := i.Decrypt(encrypted, pdu.Flags&UpdatePDUFlagSecureChecksum != 0, pdu.Data)
		if err != nil {
			return nil, err
		}
		pdu.Data = data
	}

	return &pdu, nil
}
