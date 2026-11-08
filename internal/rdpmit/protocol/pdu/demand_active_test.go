package pdu

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestDemandActiveTolerantSets: a capability set longer than this client parses (here a General set
// with 8 extra bytes) must not consume the next set, and an unknown set is skipped by its length.
func TestDemandActiveTolerantSets(t *testing.T) {
	set := func(typ uint16, body []byte) []byte {
		b := binary.LittleEndian.AppendUint16(nil, typ)
		b = binary.LittleEndian.AppendUint16(b, uint16(4+len(body)))
		return append(b, body...)
	}
	general := append(make([]byte, 24), 1, 2, 3, 4, 5, 6, 7, 8) // 24-byte body + 8 unexpected bytes
	sets := append(set(uint16(CapabilitySetTypeGeneral), general), set(0x0099, []byte{9, 9, 9, 9})...)
	sets = append(sets, set(uint16(CapabilitySetTypeVirtualChannel), []byte{0, 0, 0, 0, 0, 0, 0, 0})...)

	var b []byte
	b = binary.LittleEndian.AppendUint16(b, 0) // totalLength (not checked)
	b = binary.LittleEndian.AppendUint16(b, uint16(TypeDemandActive))
	b = binary.LittleEndian.AppendUint16(b, 1002)                // pduSource
	b = binary.LittleEndian.AppendUint32(b, 0x1234)              // shareId
	b = binary.LittleEndian.AppendUint16(b, 4)                   // lengthSourceDescriptor
	b = binary.LittleEndian.AppendUint16(b, uint16(4+len(sets))) // lengthCombinedCapabilities
	b = append(b, 'R', 'D', 'P', 0)
	b = binary.LittleEndian.AppendUint16(b, 3) // numberCapabilities
	b = binary.LittleEndian.AppendUint16(b, 0) // pad
	b = append(b, sets...)
	b = binary.LittleEndian.AppendUint32(b, 7) // sessionId

	var da ServerDemandActive
	if err := da.Deserialize(bytes.NewReader(b)); err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	if len(da.CapabilitySets) != 3 || da.CapabilitySets[2].CapabilitySetType != CapabilitySetTypeVirtualChannel ||
		da.CapabilitySets[2].VirtualChannelCapabilitySet == nil || da.SessionId != 7 {
		t.Fatalf("sets %+v session %d", da.CapabilitySets, da.SessionId)
	}
}
