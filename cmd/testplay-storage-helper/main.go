// testplay-storage-helper is an on-demand child process. It is not a Windows
// service and is not connected to the public testplay CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/Kubonsang/testplay-runner/internal/storagehelper"
	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	server := storagehelper.NewServer(vhdxstorage.NewBackend())
	if err := server.Serve(ctx, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "testplay-storage-helper: %v\n", err)
		os.Exit(1)
	}
}
