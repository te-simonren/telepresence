package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"github.com/datawire/telepresence2/pkg/client"
	"github.com/datawire/telepresence2/pkg/rpc/daemon"
)

type daemonState struct {
	cmd      *cobra.Command
	dns      string
	fallback string
	conn     *grpc.ClientConn
	grpc     daemon.DaemonClient
}

func newDaemonState(cmd *cobra.Command, dns, fallback string) (*daemonState, error) {
	ds := &daemonState{cmd: cmd, dns: dns, fallback: fallback}
	err := assertDaemonStarted()
	if err == nil {
		err = ds.connect()
	}
	return ds, err
}

func (ds *daemonState) EnsureState() (bool, error) {
	if ds.isConnected() {
		return false, nil
	}
	quitLegacyDaemon(ds.cmd.OutOrStdout())

	fmt.Fprintln(ds.cmd.OutOrStdout(), "Launching Telepresence Daemon", client.DisplayVersion())

	err := runAsRoot(client.GetExe(), []string{"daemon-foreground", ds.dns, ds.fallback})
	if err != nil {
		return false, errors.Wrap(err, "failed to launch the server")
	}

	if err = client.WaitUntilSocketAppears("daemon", client.DaemonSocketName, 10*time.Second); err != nil {
		return false, fmt.Errorf("daemon service did not start (see %s for more info)", client.Logfile)
	}
	err = ds.connect()
	return err == nil, err
}

func (ds *daemonState) DeactivateState() error {
	if !ds.isConnected() {
		return nil
	}
	fmt.Fprint(ds.cmd.OutOrStdout(), "Telepresence Daemon quitting...")
	var err error
	if client.SocketExists(client.DaemonSocketName) {
		// using context.Background() here since it's likely that the
		// command context has been cancelled.
		_, err = ds.grpc.Quit(context.Background(), &empty.Empty{})
	}
	ds.disconnect()
	if err == nil {
		err = client.WaitUntilSocketVanishes("daemon", client.DaemonSocketName, 5*time.Second)
	}
	if err == nil {
		fmt.Fprintln(ds.cmd.OutOrStdout(), "done")
	} else {
		fmt.Fprintln(ds.cmd.OutOrStdout())
	}
	return err
}

func assertDaemonStarted() error {
	if client.SocketExists(client.DaemonSocketName) {
		return nil
	}
	return errDaemonIsNotRunning
}

// withDaemon establishes a connection, calls the function with the gRPC client, and ensures
// that the connection is closed.
func withDaemon(cmd *cobra.Command, f func(daemon.DaemonClient) error) error {
	// OK with dns and fallback empty. Daemon must be up and running
	ds, err := newDaemonState(cmd, "", "")
	if err != nil {
		return err
	}
	defer ds.disconnect()
	return f(ds.grpc)
}

// isConnected returns true if a connection has been established to the daemon
func (ds *daemonState) isConnected() bool {
	return ds.conn != nil
}

// connect opens the client connection to the daemon.
func (ds *daemonState) connect() (err error) {
	if ds.conn, err = client.DialSocket(client.DaemonSocketName); err == nil {
		ds.grpc = daemon.NewDaemonClient(ds.conn)
	}
	return
}

// disconnect closes the client connection to the daemon.
func (ds *daemonState) disconnect() {
	conn := ds.conn
	ds.conn = nil
	ds.grpc = nil
	if conn != nil {
		conn.Close()
	}
}

const legacySocketName = "/var/run/edgectl.socket"

// quitLegacyDaemon ensures that an older printVersion of the daemon quits and removes the old socket.
func quitLegacyDaemon(out io.Writer) {
	if !client.SocketExists(legacySocketName) {
		return // no legacy daemon is running
	}
	if conn, err := net.Dial("unix", legacySocketName); err == nil {
		defer conn.Close()

		_, _ = io.WriteString(conn, `{"Args": ["edgectl", "quit"], "APIVersion": 1}`)
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			fmt.Fprintf(out, "Legacy daemon: %s\n", scanner.Text())
		}
	}
}
