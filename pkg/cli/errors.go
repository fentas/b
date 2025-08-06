package cli

import "errors"

var (
	// ErrNoBinaryPath indicates that no suitable binary installation path was found
	ErrNoBinaryPath = errors.New("could not find a suitable path to install binaries")
	
	// ErrUnknownBinary indicates that the specified binary is not available
	ErrUnknownBinary = errors.New("unknown binary")
	
	// ErrConfigNotFound indicates that no b.yaml configuration file was found
	ErrConfigNotFound = errors.New("no b.yaml configuration file found")
	
	// ErrInvalidConfig indicates that the configuration file is invalid
	ErrInvalidConfig = errors.New("invalid configuration file")
)
