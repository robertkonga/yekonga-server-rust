package packet

import (
	"github.com/robertkonga/yekonga-server-go/plugins/socketio/engineio/frame"
)

type Frame struct {
	FType frame.Type
	Data  []byte
}

type Packet struct {
	FType frame.Type
	PType Type
	Data  []byte
}
