package agent_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/datawire/dlib/dlog"
	rpc "github.com/datawire/telepresence2/rpc/v2/manager"
	"github.com/datawire/telepresence2/v2/cmd/traffic/cmd/agent"
)

const (
	appHost       = "appHost"
	appPort int32 = 5000
	mgrHost       = "managerHost"
)

func makeFS(t *testing.T) (*agent.Forwarder, agent.State) {
	lAddr, err := net.ResolveTCPAddr("tcp", ":1111")
	assert.NoError(t, err)

	f := agent.NewForwarder(lAddr)
	go func() {
		if err := f.Serve(context.Background(), appHost, appPort); err != nil {
			panic(err)
		}
	}()

	assert.Eventually(t, func() bool {
		_, port := f.Target()
		return port == appPort
	}, 1*time.Second, 10*time.Millisecond)

	s := agent.NewState(f, mgrHost)

	return f, s
}

func TestState_HandleIntercepts(t *testing.T) {
	ctx := dlog.NewTestContext(t, false)
	a := assert.New(t)
	f, s := makeFS(t)

	var (
		host    string
		port    int32
		cepts   []*rpc.InterceptInfo
		reviews []*rpc.ReviewInterceptRequest
	)

	// Setup worked

	host, port = f.Target()
	a.Equal(appHost, host)
	a.Equal(appPort, port)

	// Handle resets state on an empty intercept list

	reviews = s.HandleIntercepts(ctx, cepts)
	a.Len(reviews, 0)
	host, port = f.Target()
	a.Equal(appHost, host)
	a.Equal(appPort, port)

	// Prepare some intercepts..

	cepts = []*rpc.InterceptInfo{
		{
			Spec: &rpc.InterceptSpec{
				Name:      "cept1Name",
				Client:    "user@host1",
				Agent:     "agentName",
				Mechanism: "tcp",
			},
			Id: "intercept-01",
		},
		{
			Spec: &rpc.InterceptSpec{
				Name:      "cept2Name",
				Client:    "user@host2",
				Agent:     "agentName",
				Mechanism: "tcp",
			},
			Id: "intercept-02",
		},
	}

	// Handle ignores non-active and non-waiting intercepts

	cepts[0].Disposition = rpc.InterceptDispositionType_NO_PORTS
	cepts[1].Disposition = rpc.InterceptDispositionType_NO_CLIENT

	reviews = s.HandleIntercepts(ctx, cepts)
	a.Len(reviews, 0)
	host, port = f.Target()
	a.Equal(appHost, host)
	a.Equal(appPort, port)

	// Handle reviews waiting intercepts

	cepts[0].Disposition = rpc.InterceptDispositionType_WAITING
	cepts[1].Disposition = rpc.InterceptDispositionType_WAITING

	reviews = s.HandleIntercepts(ctx, cepts)
	a.Len(reviews, 2)
	host, port = f.Target()
	a.Equal(appHost, host)
	a.Equal(appPort, port)

	// Reviews are in the correct order

	a.Equal(cepts[0].Id, reviews[0].Id)
	a.Equal(cepts[1].Id, reviews[1].Id)

	// First cept was accepted, second was rejected

	a.Equal(rpc.InterceptDispositionType_ACTIVE, reviews[0].Disposition)
	a.Equal(rpc.InterceptDispositionType_AGENT_ERROR, reviews[1].Disposition)
	a.Equal("Conflicts with the currently-waiting-to-be-served intercept \"intercept-01\"", reviews[1].Message)

	// Handle updates forwarding

	cepts[0].Disposition = rpc.InterceptDispositionType_ACTIVE
	cepts[0].ManagerPort = 1138
	cepts[1].Disposition = rpc.InterceptDispositionType_WAITING

	reviews = s.HandleIntercepts(ctx, cepts)
	a.Len(reviews, 1)
	host, port = f.Target()
	a.Equal(mgrHost, host)
	a.Equal(int32(1138), port)

	a.Equal(rpc.InterceptDispositionType_AGENT_ERROR, reviews[0].Disposition)
	a.Equal("Conflicts with the currently-served intercept \"intercept-01\"", reviews[0].Message)

	// Handle resets state on an empty intercept list again

	reviews = s.HandleIntercepts(ctx, nil)
	a.Len(reviews, 0)
	host, port = f.Target()
	a.Equal(appHost, host)
	a.Equal(appPort, port)
}