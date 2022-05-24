package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"
	empty "google.golang.org/protobuf/types/known/emptypb"

	"github.com/telepresenceio/telepresence/rpc/v2/connector"
	"github.com/telepresenceio/telepresence/v2/pkg/client"
	"github.com/telepresenceio/telepresence/v2/pkg/client/cache"
	"github.com/telepresenceio/telepresence/v2/pkg/client/cli/cliutil"
	"github.com/telepresenceio/telepresence/v2/pkg/client/errcat"
)

type uninstallInfo struct {
	agent      bool
	allAgents  bool
	everything bool
	namespace  string
}

func uninstallCommand() *cobra.Command {
	ui := &uninstallInfo{}
	cmd := &cobra.Command{
		Use:  "uninstall [flags] { --agent <agents...> |--all-agents | --everything }",
		Args: ui.args,

		Short: "Uninstall telepresence agents and manager",
		RunE:  ui.run,
	}
	flags := cmd.Flags()

	flags.BoolVarP(&ui.agent, "agent", "d", false, "uninstall intercept agent on specific deployments")
	flags.BoolVarP(&ui.allAgents, "all-agents", "a", false, "uninstall intercept agent on all deployments")
	flags.BoolVarP(&ui.everything, "everything", "e", false, "uninstall agents and the traffic manager")
	flags.StringVarP(&ui.namespace, "namespace", "n", "", "If present, the namespace scope for this CLI request")

	return cmd
}

func (u *uninstallInfo) args(cmd *cobra.Command, args []string) error {
	if u.agent && u.allAgents || u.agent && u.everything || u.allAgents && u.everything {
		return errors.New("--agent, --all-agents, or --everything are mutually exclusive")
	}
	if !(u.agent || u.allAgents || u.everything) {
		return errors.New("please specify --agent, --all-agents, or --everything")
	}
	switch {
	case u.agent && len(args) == 0:
		return errors.New("at least one argument (the name of an agent) is expected")
	case !u.agent && len(args) != 0:
		return errors.New("unexpected argument(s)")
	}
	return nil
}

// uninstall
func (u *uninstallInfo) run(cmd *cobra.Command, args []string) error {
	doQuit := false
	err := withConnector(cmd, true, nil, func(ctx context.Context, cs *connectorState) error {
		ur := &connector.UninstallRequest{
			UninstallType: 0,
			Namespace:     u.namespace,
		}

		switch {
		case u.agent:
			ur.UninstallType = connector.UninstallRequest_NAMED_AGENTS
			ur.Agents = args
		case u.allAgents:
			ur.UninstallType = connector.UninstallRequest_ALL_AGENTS
		default:
			ur.UninstallType = connector.UninstallRequest_EVERYTHING
			// Ensure that the user is logged out of the system before uninstalling. We don't want
			// to install the enhanced free client if it is missing or needs an upgrade.
			_, _ = cs.userD.Logout(ctx, &empty.Empty{})
			var userDBin string
			err := client.UpdateConfig(ctx, func(cfg *client.Config, _ string) (bool, error) {
				if userDBin = cfg.Daemons.UserDaemonBinary; userDBin == "" {
					return false, nil
				}
				cfg.Daemons.UserDaemonBinary = ""
				return true, nil
			})
			if err != nil {
				return err
			}

			if userDBin != "" {
				// Restore config when we're done. We uninstall from the cluster, not the client.
				defer func() {
					_ = client.UpdateConfig(ctx, func(cfg *client.Config, _ string) (bool, error) {
						cfg.Daemons.UserDaemonBinary = userDBin
						return true, nil
					})
				}()
			}
		}
		r, err := cs.userD.Uninstall(ctx, ur)
		if err != nil {
			return err
		}
		if r.ErrorText != "" {
			ec := errcat.Unknown
			if r.ErrorCategory != 0 {
				ec = errcat.Category(r.ErrorCategory)
			}
			return ec.New(r.ErrorText)
		}

		if ur.UninstallType == connector.UninstallRequest_EVERYTHING {
			// No need to keep daemons once everything is uninstalled
			doQuit = true
			return removeClusterFromUserCache(ctx, cs.ConnectInfo)
		}
		return nil
	})
	if err == nil && doQuit {
		err = cliutil.Disconnect(cmd.Context(), true, true)
	}
	return err
}

func removeClusterFromUserCache(ctx context.Context, connInfo *connector.ConnectInfo) (err error) {
	// Delete the ingress info for the cluster if it exists.
	ingresses, err := cache.LoadIngressesFromUserCache(ctx)
	if err != nil {
		return err
	}

	key := connInfo.ClusterServer + "/" + connInfo.ClusterContext
	if _, ok := ingresses[key]; ok {
		delete(ingresses, key)
		if err = cache.SaveIngressesToUserCache(ctx, ingresses); err != nil {
			return err
		}
	}
	return nil
}
