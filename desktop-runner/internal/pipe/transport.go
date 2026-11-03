package pipe

import (
	"context"
	"io"
)

type Acceptor interface {
	Accept(context.Context) (io.ReadWriteCloser, error)
	Close() error
}
