//go:build !windows

package pipe

// Only Windows composes the named pipe. No Linux listener or fallback exists.
func Listen() (Acceptor, error) { return nil, ErrDisturbance }
