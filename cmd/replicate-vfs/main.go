//go:build SQLITE3VFS_LOADABLE_EXT
// +build SQLITE3VFS_LOADABLE_EXT

package main

// import C is necessary export to the c-archive .a file

/*
typedef long long int sqlite3_int64;
typedef unsigned long long int sqlite3_uint64;
*/
import "C"

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
	"unsafe"

	"github.com/psanford/sqlite3vfs"

	"github.com/hanzoai/replicate"

	// Import all replica backends to register their URL factories.
	_ "github.com/hanzoai/replicate/abs"
	_ "github.com/hanzoai/replicate/file"
	_ "github.com/hanzoai/replicate/gs"
	_ "github.com/hanzoai/replicate/nats"
	_ "github.com/hanzoai/replicate/oss"
	_ "github.com/hanzoai/replicate/s3"
	_ "github.com/hanzoai/replicate/sftp"
	_ "github.com/hanzoai/replicate/webdav"
)

func main() {}

// envWithFallback checks REPLICATE_ env var first, falls back to LITESTREAM_ for compat.
func envWithFallback(suffix string) string {
	if v := os.Getenv("REPLICATE_" + suffix); v != "" {
		return v
	}
	return os.Getenv("LITESTREAM_" + suffix)
}

//export ReplicateVFSRegister
func ReplicateVFSRegister() *C.char {
	var client replicate.ReplicaClient
	var err error

	replicaURL := envWithFallback("REPLICA_URL")
	if replicaURL == "" {
		return C.CString("REPLICATE_REPLICA_URL environment variable required")
	}

	client, err = replicate.NewReplicaClientFromURL(replicaURL)
	if err != nil {
		return C.CString(fmt.Sprintf("failed to create replica client: %s", err))
	}

	// Initialize the client.
	if err := client.Init(context.Background()); err != nil {
		return C.CString(fmt.Sprintf("failed to initialize replica client: %s", err))
	}

	var level slog.Level
	switch strings.ToUpper(envWithFallback("LOG_LEVEL")) {
	case "DEBUG":
		level = slog.LevelDebug
	default:
		level = slog.LevelInfo
	}

	var logOutput io.Writer = os.Stdout
	if logFile := envWithFallback("LOG_FILE"); logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return C.CString(fmt.Sprintf("failed to open log file: %s", err))
		}
		logOutput = f
	}
	logger := slog.New(slog.NewTextHandler(logOutput, &slog.HandlerOptions{Level: level}))

	vfs := replicate.NewVFS(client, logger)

	// Configure write support if enabled.
	if strings.ToLower(envWithFallback("WRITE_ENABLED")) == "true" {
		vfs.WriteEnabled = true

		if s := envWithFallback("SYNC_INTERVAL"); s != "" {
			d, err := time.ParseDuration(s)
			if err != nil {
				return C.CString(fmt.Sprintf("invalid REPLICATE_SYNC_INTERVAL: %s", err))
			}
			vfs.WriteSyncInterval = d
		}

		if s := envWithFallback("BUFFER_PATH"); s != "" {
			vfs.WriteBufferPath = s
		}
	}

	// Configure hydration support if enabled.
	if strings.ToLower(envWithFallback("HYDRATION_ENABLED")) == "true" {
		vfs.HydrationEnabled = true

		if s := envWithFallback("HYDRATION_PATH"); s != "" {
			vfs.HydrationPath = s
		}
	}

	if err := sqlite3vfs.RegisterVFS("replicate", vfs); err != nil {
		return C.CString(fmt.Sprintf("failed to register VFS: %s", err))
	}

	return nil
}

//export GoReplicateRegisterConnection
func GoReplicateRegisterConnection(dbPtr unsafe.Pointer, fileID C.sqlite3_uint64) *C.char {
	if err := replicate.RegisterVFSConnection(uintptr(dbPtr), uint64(fileID)); err != nil {
		return C.CString(err.Error())
	}
	return nil
}

//export GoReplicateUnregisterConnection
func GoReplicateUnregisterConnection(dbPtr unsafe.Pointer) *C.char {
	replicate.UnregisterVFSConnection(uintptr(dbPtr))
	return nil
}

//export GoReplicateSetTime
func GoReplicateSetTime(dbPtr unsafe.Pointer, timestamp *C.char) *C.char {
	if timestamp == nil {
		return C.CString("timestamp required")
	}
	if err := replicate.SetVFSConnectionTime(uintptr(dbPtr), C.GoString(timestamp)); err != nil {
		return C.CString(err.Error())
	}
	return nil
}

//export GoReplicateResetTime
func GoReplicateResetTime(dbPtr unsafe.Pointer) *C.char {
	if err := replicate.ResetVFSConnectionTime(uintptr(dbPtr)); err != nil {
		return C.CString(err.Error())
	}
	return nil
}

//export GoReplicateTime
func GoReplicateTime(dbPtr unsafe.Pointer, out **C.char) *C.char {
	value, err := replicate.GetVFSConnectionTime(uintptr(dbPtr))
	if err != nil {
		return C.CString(err.Error())
	}
	if out != nil {
		*out = C.CString(value)
	}
	return nil
}

//export GoReplicateTxid
func GoReplicateTxid(dbPtr unsafe.Pointer, out **C.char) *C.char {
	value, err := replicate.GetVFSConnectionTXID(uintptr(dbPtr))
	if err != nil {
		return C.CString(err.Error())
	}
	if out != nil {
		*out = C.CString(value)
	}
	return nil
}

//export GoReplicateLag
func GoReplicateLag(dbPtr unsafe.Pointer, out *C.sqlite3_int64) *C.char {
	value, err := replicate.GetVFSConnectionLag(uintptr(dbPtr))
	if err != nil {
		return C.CString(err.Error())
	}
	if out != nil {
		*out = C.sqlite3_int64(value)
	}
	return nil
}
