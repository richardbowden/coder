package coderd_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"cdr.dev/slog"
	"cdr.dev/slog/sloggers/slogtest"
	"github.com/coder/coder/agent"
	"github.com/coder/coder/coderd/coderdtest"
	"github.com/coder/coder/coderd/database"
	"github.com/coder/coder/coderd/gitauth"
	"github.com/coder/coder/codersdk"
	"github.com/coder/coder/codersdk/agentsdk"
	"github.com/coder/coder/provisioner/echo"
	"github.com/coder/coder/provisionersdk/proto"
	"github.com/coder/coder/testutil"
)

func TestWorkspaceAgent(t *testing.T) {
	t.Parallel()
	t.Run("Connect", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		tmpDir := t.TempDir()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id:        uuid.NewString(),
								Directory: tmpDir,
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
		defer cancel()

		workspace, err := client.Workspace(ctx, workspace.ID)
		require.NoError(t, err)
		require.Equal(t, tmpDir, workspace.LatestBuild.Resources[0].Agents[0].Directory)
		_, err = client.WorkspaceAgent(ctx, workspace.LatestBuild.Resources[0].Agents[0].ID)
		require.NoError(t, err)
	})
	t.Run("HasFallbackTroubleshootingURL", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		tmpDir := t.TempDir()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id:        uuid.NewString(),
								Directory: tmpDir,
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitMedium)
		defer cancel()

		workspace, err := client.Workspace(ctx, workspace.ID)
		require.NoError(t, err)
		require.NotEmpty(t, workspace.LatestBuild.Resources[0].Agents[0].TroubleshootingURL)
		t.Log(workspace.LatestBuild.Resources[0].Agents[0].TroubleshootingURL)
	})
	t.Run("Timeout", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		tmpDir := t.TempDir()

		wantTroubleshootingURL := "https://example.com/troubleshoot"

		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id:        uuid.NewString(),
								Directory: tmpDir,
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
								ConnectionTimeoutSeconds: 1,
								TroubleshootingUrl:       wantTroubleshootingURL,
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitMedium)
		defer cancel()

		var err error
		testutil.Eventually(ctx, t, func(ctx context.Context) (done bool) {
			workspace, err = client.Workspace(ctx, workspace.ID)
			if !assert.NoError(t, err) {
				return false
			}
			return workspace.LatestBuild.Resources[0].Agents[0].Status == codersdk.WorkspaceAgentTimeout
		}, testutil.IntervalMedium, "agent status timeout")

		require.Equal(t, wantTroubleshootingURL, workspace.LatestBuild.Resources[0].Agents[0].TroubleshootingURL)
	})
}

func TestWorkspaceAgentStartupLogs(t *testing.T) {
	t.Parallel()
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitMedium)
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id: uuid.NewString(),
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		build := coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)
		err := agentClient.PatchStartupLogs(ctx, agentsdk.PatchStartupLogs{
			Logs: []agentsdk.StartupLog{
				{
					CreatedAt: database.Now(),
					Output:    "testing",
				},
				{
					CreatedAt: database.Now(),
					Output:    "testing2",
				},
			},
		})
		require.NoError(t, err)

		logs, closer, err := client.WorkspaceAgentStartupLogsAfter(ctx, build.Resources[0].Agents[0].ID, 0)
		require.NoError(t, err)
		defer func() {
			_ = closer.Close()
		}()
		var logChunk []codersdk.WorkspaceAgentStartupLog
		select {
		case <-ctx.Done():
		case logChunk = <-logs:
		}
		require.NoError(t, ctx.Err())
		require.Len(t, logChunk, 2) // No EOF.
		require.Equal(t, "testing", logChunk[0].Output)
		require.Equal(t, "testing2", logChunk[1].Output)
	})
	t.Run("PublishesOnOverflow", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitMedium)
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id: uuid.NewString(),
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		updates, err := client.WatchWorkspace(ctx, workspace.ID)
		require.NoError(t, err)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)
		err = agentClient.PatchStartupLogs(ctx, agentsdk.PatchStartupLogs{
			Logs: []agentsdk.StartupLog{{
				CreatedAt: database.Now(),
				Output:    strings.Repeat("a", (1<<20)+1),
			}},
		})
		var apiError *codersdk.Error
		require.ErrorAs(t, err, &apiError)
		require.Equal(t, http.StatusRequestEntityTooLarge, apiError.StatusCode())

		// It's possible we have multiple updates queued, but that's alright, we just
		// wait for the one where it overflows.
		for {
			var update codersdk.Workspace
			select {
			case <-ctx.Done():
				t.FailNow()
			case update = <-updates:
			}
			if update.LatestBuild.Resources[0].Agents[0].StartupLogsOverflowed {
				break
			}
		}
	})
	t.Run("AllowEOFAfterOverflowAndCloseFollowWebsocket", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitMedium)
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id: uuid.NewString(),
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		build := coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		updates, err := client.WatchWorkspace(ctx, workspace.ID)
		require.NoError(t, err)

		logs, closeLogs, err := client.WorkspaceAgentStartupLogsAfter(ctx, build.Resources[0].Agents[0].ID, 0)
		require.NoError(t, err)
		defer closeLogs.Close()

		wantLogs := []codersdk.WorkspaceAgentStartupLog{
			{
				CreatedAt: database.Now(),
				Output:    "testing",
				Level:     "info",
			},
			{
				CreatedAt: database.Now().Add(time.Minute),
				Level:     "info",
				EOF:       true,
			},
		}

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		var convertedLogs []agentsdk.StartupLog
		for _, log := range wantLogs {
			convertedLogs = append(convertedLogs, agentsdk.StartupLog{
				CreatedAt: log.CreatedAt,
				Output:    log.Output,
				Level:     log.Level,
				EOF:       log.EOF,
			})
		}
		initialLogs := convertedLogs[:len(convertedLogs)-1]
		eofLog := convertedLogs[len(convertedLogs)-1]
		err = agentClient.PatchStartupLogs(ctx, agentsdk.PatchStartupLogs{Logs: initialLogs})
		require.NoError(t, err)

		overflowLogs := []agentsdk.StartupLog{
			{
				CreatedAt: database.Now(),
				Output:    strings.Repeat("a", (1<<20)+1),
			},
			eofLog, // Include EOF which will be discarded due to overflow.
		}
		err = agentClient.PatchStartupLogs(ctx, agentsdk.PatchStartupLogs{Logs: overflowLogs})
		var apiError *codersdk.Error
		require.ErrorAs(t, err, &apiError)
		require.Equal(t, http.StatusRequestEntityTooLarge, apiError.StatusCode())

		// It's possible we have multiple updates queued, but that's alright, we just
		// wait for the one where it overflows.
		for {
			var update codersdk.Workspace
			select {
			case <-ctx.Done():
				require.Fail(t, "timed out waiting for overflow")
			case update = <-updates:
			}
			if update.LatestBuild.Resources[0].Agents[0].StartupLogsOverflowed {
				break
			}
		}

		// Now we should still be able to send the EOF.
		err = agentClient.PatchStartupLogs(ctx, agentsdk.PatchStartupLogs{Logs: []agentsdk.StartupLog{eofLog}})
		require.NoError(t, err)

		var gotLogs []codersdk.WorkspaceAgentStartupLog
	logsLoop:
		for {
			select {
			case <-ctx.Done():
				require.Fail(t, "timed out waiting for logs")
			case l, ok := <-logs:
				if !ok {
					break logsLoop
				}
				gotLogs = append(gotLogs, l...)
			}
		}
		for i := range gotLogs {
			gotLogs[i].ID = 0 // Ignore ID for comparison.
		}
		require.Equal(t, wantLogs, gotLogs)
	})
	t.Run("CloseAfterLifecycleStateIsNotRunning", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitMedium)
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id: uuid.NewString(),
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		build := coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		logs, closer, err := client.WorkspaceAgentStartupLogsAfter(ctx, build.Resources[0].Agents[0].ID, 0)
		require.NoError(t, err)
		defer func() {
			_ = closer.Close()
		}()

		err = agentClient.PatchStartupLogs(ctx, agentsdk.PatchStartupLogs{
			Logs: []agentsdk.StartupLog{
				{
					CreatedAt: database.Now(),
					Output:    "testing",
				},
			},
		})
		require.NoError(t, err)

		err = agentClient.PostLifecycle(ctx, agentsdk.PostLifecycleRequest{
			State: codersdk.WorkspaceAgentLifecycleReady,
		})
		require.NoError(t, err)

		for {
			select {
			case <-ctx.Done():
				require.Fail(t, "timed out waiting for logs EOF")
			case l := <-logs:
				for _, log := range l {
					if log.EOF {
						// Success.
						return
					}
				}
			}
		}
	})
	t.Run("NoLogAfterEOF", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitMedium)
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id: uuid.NewString(),
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		_ = coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		err := agentClient.PatchStartupLogs(ctx, agentsdk.PatchStartupLogs{
			Logs: []agentsdk.StartupLog{
				{
					CreatedAt: database.Now(),
					EOF:       true,
				},
			},
		})
		require.NoError(t, err)

		err = agentClient.PatchStartupLogs(ctx, agentsdk.PatchStartupLogs{
			Logs: []agentsdk.StartupLog{
				{
					CreatedAt: database.Now(),
					Output:    "testing",
				},
			},
		})
		require.Error(t, err, "insert after EOF should not succeed")
	})
}

func TestWorkspaceAgentListen(t *testing.T) {
	t.Parallel()

	t.Run("Connect", func(t *testing.T) {
		t.Parallel()

		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)
		agentCloser := agent.New(agent.Options{
			Client: agentClient,
			Logger: slogtest.Make(t, nil).Named("agent").Leveled(slog.LevelDebug),
		})
		defer func() {
			_ = agentCloser.Close()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
		defer cancel()

		resources := coderdtest.AwaitWorkspaceAgents(t, client, workspace.ID)
		conn, err := client.DialWorkspaceAgent(ctx, resources[0].Agents[0].ID, nil)
		require.NoError(t, err)
		defer func() {
			_ = conn.Close()
		}()
		conn.AwaitReachable(ctx)
	})

	t.Run("FailNonLatestBuild", func(t *testing.T) {
		t.Parallel()

		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})

		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})

		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		version = coderdtest.UpdateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id: uuid.NewString(),
								Auth: &proto.Agent_Token{
									Token: uuid.NewString(),
								},
							}},
						}},
					},
				},
			}},
		}, template.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)

		ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
		defer cancel()

		stopBuild, err := client.CreateWorkspaceBuild(ctx, workspace.ID, codersdk.CreateWorkspaceBuildRequest{
			TemplateVersionID: version.ID,
			Transition:        codersdk.WorkspaceTransitionStop,
		})
		require.NoError(t, err)
		coderdtest.AwaitWorkspaceBuildJob(t, client, stopBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		_, err = agentClient.Listen(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, "build is outdated")
	})
}

func TestWorkspaceAgentTailnet(t *testing.T) {
	t.Parallel()
	client, daemonCloser := coderdtest.NewWithProvisionerCloser(t, nil)
	user := coderdtest.CreateFirstUser(t, client)
	authToken := uuid.NewString()
	version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
		Parse:          echo.ParseComplete,
		ProvisionPlan:  echo.ProvisionComplete,
		ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
	})
	template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
	coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
	workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
	coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)
	daemonCloser.Close()

	agentClient := agentsdk.New(client.URL)
	agentClient.SetSessionToken(authToken)
	agentCloser := agent.New(agent.Options{
		Client: agentClient,
		Logger: slogtest.Make(t, nil).Named("agent").Leveled(slog.LevelDebug),
	})
	defer agentCloser.Close()
	resources := coderdtest.AwaitWorkspaceAgents(t, client, workspace.ID)

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	conn, err := client.DialWorkspaceAgent(ctx, resources[0].Agents[0].ID, &codersdk.DialWorkspaceAgentOptions{
		Logger: slogtest.Make(t, nil).Named("client").Leveled(slog.LevelDebug),
	})
	require.NoError(t, err)
	defer conn.Close()
	sshClient, err := conn.SSHClient(ctx)
	require.NoError(t, err)
	session, err := sshClient.NewSession()
	require.NoError(t, err)
	output, err := session.CombinedOutput("echo test")
	require.NoError(t, err)
	_ = session.Close()
	_ = sshClient.Close()
	_ = conn.Close()
	require.Equal(t, "test", strings.TrimSpace(string(output)))
}

func TestWorkspaceAgentListeningPorts(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T, apps []*proto.App) (*codersdk.Client, uint16, uuid.UUID) {
		t.Helper()

		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		coderdPort, err := strconv.Atoi(client.URL.Port())
		require.NoError(t, err)

		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id: uuid.NewString(),
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
								Apps: apps,
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)
		agentCloser := agent.New(agent.Options{
			Client: agentClient,
			Logger: slogtest.Make(t, nil).Named("agent").Leveled(slog.LevelDebug),
		})
		t.Cleanup(func() {
			_ = agentCloser.Close()
		})
		resources := coderdtest.AwaitWorkspaceAgents(t, client, workspace.ID)

		return client, uint16(coderdPort), resources[0].Agents[0].ID
	}

	willFilterPort := func(port int) bool {
		if port < codersdk.WorkspaceAgentMinimumListeningPort || port > 65535 {
			return true
		}
		if _, ok := codersdk.WorkspaceAgentIgnoredListeningPorts[uint16(port)]; ok {
			return true
		}

		return false
	}

	generateUnfilteredPort := func(t *testing.T) (net.Listener, uint16) {
		var (
			l    net.Listener
			port uint16
		)
		require.Eventually(t, func() bool {
			var err error
			l, err = net.Listen("tcp", "localhost:0")
			if err != nil {
				return false
			}
			tcpAddr, _ := l.Addr().(*net.TCPAddr)
			if willFilterPort(tcpAddr.Port) {
				_ = l.Close()
				return false
			}
			t.Cleanup(func() {
				_ = l.Close()
			})

			port = uint16(tcpAddr.Port)
			return true
		}, testutil.WaitShort, testutil.IntervalFast)

		return l, port
	}

	generateFilteredPort := func(t *testing.T) (net.Listener, uint16) {
		var (
			l    net.Listener
			port uint16
		)
		require.Eventually(t, func() bool {
			for ignoredPort := range codersdk.WorkspaceAgentIgnoredListeningPorts {
				if ignoredPort < 1024 || ignoredPort == 5432 {
					continue
				}

				var err error
				l, err = net.Listen("tcp", fmt.Sprintf("localhost:%d", ignoredPort))
				if err != nil {
					continue
				}
				t.Cleanup(func() {
					_ = l.Close()
				})

				port = ignoredPort
				return true
			}

			return false
		}, testutil.WaitShort, testutil.IntervalFast)

		return l, port
	}

	t.Run("LinuxAndWindows", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
			t.Skip("only runs on linux and windows")
			return
		}

		t.Run("OK", func(t *testing.T) {
			t.Parallel()

			client, coderdPort, agentID := setup(t, nil)

			ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
			defer cancel()

			// Generate a random unfiltered port.
			l, lPort := generateUnfilteredPort(t)

			// List ports and ensure that the port we expect to see is there.
			res, err := client.WorkspaceAgentListeningPorts(ctx, agentID)
			require.NoError(t, err)

			expected := map[uint16]bool{
				// expect the listener we made
				lPort: false,
				// expect the coderdtest server
				coderdPort: false,
			}
			for _, port := range res.Ports {
				if port.Network == "tcp" {
					if val, ok := expected[port.Port]; ok {
						if val {
							t.Fatalf("expected to find TCP port %d only once in response", port.Port)
						}
					}
					expected[port.Port] = true
				}
			}
			for port, found := range expected {
				if !found {
					t.Fatalf("expected to find TCP port %d in response", port)
				}
			}

			// Close the listener and check that the port is no longer in the response.
			require.NoError(t, l.Close())
			time.Sleep(2 * time.Second) // avoid cache
			res, err = client.WorkspaceAgentListeningPorts(ctx, agentID)
			require.NoError(t, err)

			for _, port := range res.Ports {
				if port.Network == "tcp" && port.Port == lPort {
					t.Fatalf("expected to not find TCP port %d in response", lPort)
				}
			}
		})

		t.Run("Filter", func(t *testing.T) {
			t.Parallel()

			// Generate an unfiltered port that we will create an app for and
			// should not exist in the response.
			_, appLPort := generateUnfilteredPort(t)
			app := &proto.App{
				Slug: "test-app",
				Url:  fmt.Sprintf("http://localhost:%d", appLPort),
			}

			// Generate a filtered port that should not exist in the response.
			_, filteredLPort := generateFilteredPort(t)

			client, coderdPort, agentID := setup(t, []*proto.App{app})

			ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
			defer cancel()

			res, err := client.WorkspaceAgentListeningPorts(ctx, agentID)
			require.NoError(t, err)

			sawCoderdPort := false
			for _, port := range res.Ports {
				if port.Network == "tcp" {
					if port.Port == appLPort {
						t.Fatalf("expected to not find TCP port (app port) %d in response", appLPort)
					}
					if port.Port == filteredLPort {
						t.Fatalf("expected to not find TCP port (filtered port) %d in response", filteredLPort)
					}
					if port.Port == coderdPort {
						sawCoderdPort = true
					}
				}
			}
			if !sawCoderdPort {
				t.Fatalf("expected to find TCP port (coderd port) %d in response", coderdPort)
			}
		})
	})

	t.Run("Darwin", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS != "darwin" {
			t.Skip("only runs on darwin")
			return
		}

		client, _, agentID := setup(t, nil)

		ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
		defer cancel()

		// Create a TCP listener on a random port.
		l, err := net.Listen("tcp", "localhost:0")
		require.NoError(t, err)
		defer l.Close()

		// List ports and ensure that the list is empty because we're on darwin.
		res, err := client.WorkspaceAgentListeningPorts(ctx, agentID)
		require.NoError(t, err)
		require.Len(t, res.Ports, 0)
	})
}

func TestWorkspaceAgentAppHealth(t *testing.T) {
	t.Parallel()
	client := coderdtest.New(t, &coderdtest.Options{
		IncludeProvisionerDaemon: true,
	})
	user := coderdtest.CreateFirstUser(t, client)
	authToken := uuid.NewString()
	apps := []*proto.App{
		{
			Slug:    "code-server",
			Command: "some-command",
			Url:     "http://localhost:3000",
			Icon:    "/code.svg",
		},
		{
			Slug:        "code-server-2",
			DisplayName: "code-server-2",
			Command:     "some-command",
			Url:         "http://localhost:3000",
			Icon:        "/code.svg",
			Healthcheck: &proto.Healthcheck{
				Url:       "http://localhost:3000",
				Interval:  5,
				Threshold: 6,
			},
		},
	}
	version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
		Parse: echo.ParseComplete,
		ProvisionApply: []*proto.Provision_Response{{
			Type: &proto.Provision_Response_Complete{
				Complete: &proto.Provision_Complete{
					Resources: []*proto.Resource{{
						Name: "example",
						Type: "aws_instance",
						Agents: []*proto.Agent{{
							Id: uuid.NewString(),
							Auth: &proto.Agent_Token{
								Token: authToken,
							},
							Apps: apps,
						}},
					}},
				},
			},
		}},
	})
	coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
	template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
	workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
	coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

	ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
	defer cancel()

	agentClient := agentsdk.New(client.URL)
	agentClient.SetSessionToken(authToken)

	manifest, err := agentClient.Manifest(ctx)
	require.NoError(t, err)
	require.EqualValues(t, codersdk.WorkspaceAppHealthDisabled, manifest.Apps[0].Health)
	require.EqualValues(t, codersdk.WorkspaceAppHealthInitializing, manifest.Apps[1].Health)
	err = agentClient.PostAppHealth(ctx, agentsdk.PostAppHealthsRequest{})
	require.Error(t, err)
	// empty
	err = agentClient.PostAppHealth(ctx, agentsdk.PostAppHealthsRequest{})
	require.Error(t, err)
	// healthcheck disabled
	err = agentClient.PostAppHealth(ctx, agentsdk.PostAppHealthsRequest{
		Healths: map[uuid.UUID]codersdk.WorkspaceAppHealth{
			manifest.Apps[0].ID: codersdk.WorkspaceAppHealthInitializing,
		},
	})
	require.Error(t, err)
	// invalid value
	err = agentClient.PostAppHealth(ctx, agentsdk.PostAppHealthsRequest{
		Healths: map[uuid.UUID]codersdk.WorkspaceAppHealth{
			manifest.Apps[1].ID: codersdk.WorkspaceAppHealth("bad-value"),
		},
	})
	require.Error(t, err)
	// update to healthy
	err = agentClient.PostAppHealth(ctx, agentsdk.PostAppHealthsRequest{
		Healths: map[uuid.UUID]codersdk.WorkspaceAppHealth{
			manifest.Apps[1].ID: codersdk.WorkspaceAppHealthHealthy,
		},
	})
	require.NoError(t, err)
	manifest, err = agentClient.Manifest(ctx)
	require.NoError(t, err)
	require.EqualValues(t, codersdk.WorkspaceAppHealthHealthy, manifest.Apps[1].Health)
	// update to unhealthy
	err = agentClient.PostAppHealth(ctx, agentsdk.PostAppHealthsRequest{
		Healths: map[uuid.UUID]codersdk.WorkspaceAppHealth{
			manifest.Apps[1].ID: codersdk.WorkspaceAppHealthUnhealthy,
		},
	})
	require.NoError(t, err)
	manifest, err = agentClient.Manifest(ctx)
	require.NoError(t, err)
	require.EqualValues(t, codersdk.WorkspaceAppHealthUnhealthy, manifest.Apps[1].Health)
}

// nolint:bodyclose
func TestWorkspaceAgentsGitAuth(t *testing.T) {
	t.Parallel()
	t.Run("NoMatchingConfig", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs:           []*gitauth.Config{},
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)
		_, err := agentClient.GitAuth(context.Background(), "github.com", false)
		var apiError *codersdk.Error
		require.ErrorAs(t, err, &apiError)
		require.Equal(t, http.StatusNotFound, apiError.StatusCode())
	})
	t.Run("ReturnsURL", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				OAuth2Config: &testutil.OAuth2Config{},
				ID:           "github",
				Regex:        regexp.MustCompile(`github\.com`),
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:         echo.ParseComplete,
			ProvisionPlan: echo.ProvisionComplete,
			ProvisionApply: []*proto.Provision_Response{{
				Type: &proto.Provision_Response_Complete{
					Complete: &proto.Provision_Complete{
						Resources: []*proto.Resource{{
							Name: "example",
							Type: "aws_instance",
							Agents: []*proto.Agent{{
								Id: uuid.NewString(),
								Auth: &proto.Agent_Token{
									Token: authToken,
								},
							}},
						}},
					},
				},
			}},
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)
		token, err := agentClient.GitAuth(context.Background(), "github.com/asd/asd", false)
		require.NoError(t, err)
		require.True(t, strings.HasSuffix(token.URL, fmt.Sprintf("/gitauth/%s", "github")))
	})
	t.Run("UnauthorizedCallback", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				OAuth2Config: &testutil.OAuth2Config{},
				ID:           "github",
				Regex:        regexp.MustCompile(`github\.com`),
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		resp := coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	})
	t.Run("AuthorizedCallback", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				OAuth2Config: &testutil.OAuth2Config{},
				ID:           "github",
				Regex:        regexp.MustCompile(`github\.com`),
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		_ = coderdtest.CreateFirstUser(t, client)
		resp := coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
		location, err := resp.Location()
		require.NoError(t, err)
		require.Equal(t, "/gitauth", location.Path)

		// Callback again to simulate updating the token.
		resp = coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
	})
	t.Run("ValidateURL", func(t *testing.T) {
		t.Parallel()
		ctx := testutil.Context(t, testutil.WaitLong)

		srv := httptest.NewServer(nil)
		defer srv.Close()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				ValidateURL:  srv.URL,
				OAuth2Config: &testutil.OAuth2Config{},
				ID:           "github",
				Regex:        regexp.MustCompile(`github\.com`),
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		resp := coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)

		// If the validation URL says unauthorized, the callback
		// URL to re-authenticate should be returned.
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		res, err := agentClient.GitAuth(ctx, "github.com/asd/asd", false)
		require.NoError(t, err)
		require.NotEmpty(t, res.URL)

		// If the validation URL gives a non-OK status code, this
		// should be treated as an internal server error.
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("Something went wrong!"))
		})
		_, err = agentClient.GitAuth(ctx, "github.com/asd/asd", false)
		var apiError *codersdk.Error
		require.ErrorAs(t, err, &apiError)
		require.Equal(t, http.StatusInternalServerError, apiError.StatusCode())
		require.Equal(t, "validate git auth token: status 403: body: Something went wrong!", apiError.Detail)
	})

	t.Run("ExpiredNoRefresh", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				OAuth2Config: &testutil.OAuth2Config{
					Token: &oauth2.Token{
						AccessToken:  "token",
						RefreshToken: "something",
						Expiry:       database.Now().Add(-time.Hour),
					},
				},
				ID:        "github",
				Regex:     regexp.MustCompile(`github\.com`),
				Type:      codersdk.GitProviderGitHub,
				NoRefresh: true,
			}},
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		token, err := agentClient.GitAuth(context.Background(), "github.com/asd/asd", false)
		require.NoError(t, err)
		require.NotEmpty(t, token.URL)

		// In the configuration, we set our OAuth provider
		// to return an expired token. Coder consumes this
		// and stores it.
		resp := coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)

		// Because the token is expired and `NoRefresh` is specified,
		// a redirect URL should be returned again.
		token, err = agentClient.GitAuth(context.Background(), "github.com/asd/asd", false)
		require.NoError(t, err)
		require.NotEmpty(t, token.URL)
	})

	t.Run("FullFlow", func(t *testing.T) {
		t.Parallel()
		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
			GitAuthConfigs: []*gitauth.Config{{
				OAuth2Config: &testutil.OAuth2Config{},
				ID:           "github",
				Regex:        regexp.MustCompile(`github\.com`),
				Type:         codersdk.GitProviderGitHub,
			}},
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		token, err := agentClient.GitAuth(context.Background(), "github.com/asd/asd", false)
		require.NoError(t, err)
		require.NotEmpty(t, token.URL)

		// Start waiting for the token callback...
		tokenChan := make(chan agentsdk.GitAuthResponse, 1)
		go func() {
			token, err := agentClient.GitAuth(context.Background(), "github.com/asd/asd", true)
			assert.NoError(t, err)
			tokenChan <- token
		}()

		time.Sleep(250 * time.Millisecond)

		resp := coderdtest.RequestGitAuthCallback(t, "github", client)
		require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
		token = <-tokenChan
		require.Equal(t, "access_token", token.Username)

		token, err = agentClient.GitAuth(context.Background(), "github.com/asd/asd", false)
		require.NoError(t, err)
	})
}

func TestWorkspaceAgentReportStats(t *testing.T) {
	t.Parallel()

	t.Run("OK", func(t *testing.T) {
		t.Parallel()

		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		_, err := agentClient.PostStats(context.Background(), &agentsdk.Stats{
			ConnectionsByProto:          map[string]int64{"TCP": 1},
			ConnectionCount:             1,
			RxPackets:                   1,
			RxBytes:                     1,
			TxPackets:                   1,
			TxBytes:                     1,
			SessionCountVSCode:          1,
			SessionCountJetBrains:       1,
			SessionCountReconnectingPTY: 1,
			SessionCountSSH:             1,
			ConnectionMedianLatencyMS:   10,
		})
		require.NoError(t, err)

		newWorkspace, err := client.Workspace(context.Background(), workspace.ID)
		require.NoError(t, err)

		assert.True(t,
			newWorkspace.LastUsedAt.After(workspace.LastUsedAt),
			"%s is not after %s", newWorkspace.LastUsedAt, workspace.LastUsedAt,
		)
	})
}

func TestWorkspaceAgent_LifecycleState(t *testing.T) {
	t.Parallel()

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		for _, res := range workspace.LatestBuild.Resources {
			for _, a := range res.Agents {
				require.Equal(t, codersdk.WorkspaceAgentLifecycleCreated, a.LifecycleState)
			}
		}

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		tests := []struct {
			state   codersdk.WorkspaceAgentLifecycle
			wantErr bool
		}{
			{codersdk.WorkspaceAgentLifecycleCreated, false},
			{codersdk.WorkspaceAgentLifecycleStarting, false},
			{codersdk.WorkspaceAgentLifecycleStartTimeout, false},
			{codersdk.WorkspaceAgentLifecycleStartError, false},
			{codersdk.WorkspaceAgentLifecycleReady, false},
			{codersdk.WorkspaceAgentLifecycleShuttingDown, false},
			{codersdk.WorkspaceAgentLifecycleShutdownTimeout, false},
			{codersdk.WorkspaceAgentLifecycleShutdownError, false},
			{codersdk.WorkspaceAgentLifecycleOff, false},
			{codersdk.WorkspaceAgentLifecycle("nonexistent_state"), true},
			{codersdk.WorkspaceAgentLifecycle(""), true},
		}
		//nolint:paralleltest // No race between setting the state and getting the workspace.
		for _, tt := range tests {
			tt := tt
			t.Run(string(tt.state), func(t *testing.T) {
				ctx := testutil.Context(t, testutil.WaitLong)

				err := agentClient.PostLifecycle(ctx, agentsdk.PostLifecycleRequest{
					State: tt.state,
				})
				if tt.wantErr {
					require.Error(t, err)
					return
				}
				require.NoError(t, err, "post lifecycle state %q", tt.state)

				workspace, err = client.Workspace(ctx, workspace.ID)
				require.NoError(t, err, "get workspace")

				for _, res := range workspace.LatestBuild.Resources {
					for _, agent := range res.Agents {
						require.Equal(t, tt.state, agent.LifecycleState)
					}
				}
			})
		}
	})
}

func TestWorkspaceAgent_Metadata(t *testing.T) {
	t.Parallel()

	client := coderdtest.New(t, &coderdtest.Options{
		IncludeProvisionerDaemon: true,
	})
	user := coderdtest.CreateFirstUser(t, client)
	authToken := uuid.NewString()
	version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
		Parse:         echo.ParseComplete,
		ProvisionPlan: echo.ProvisionComplete,
		ProvisionApply: []*proto.Provision_Response{{
			Type: &proto.Provision_Response_Complete{
				Complete: &proto.Provision_Complete{
					Resources: []*proto.Resource{{
						Name: "example",
						Type: "aws_instance",
						Agents: []*proto.Agent{{
							Metadata: []*proto.Agent_Metadata{
								{
									DisplayName: "First Meta",
									Key:         "foo1",
									Script:      "echo hi",
									Interval:    10,
									Timeout:     3,
								},
								{
									DisplayName: "Second Meta",
									Key:         "foo2",
									Script:      "echo howdy",
									Interval:    10,
									Timeout:     3,
								},
								{
									DisplayName: "TooLong",
									Key:         "foo3",
									Script:      "echo howdy",
									Interval:    10,
									Timeout:     3,
								},
							},
							Id: uuid.NewString(),
							Auth: &proto.Agent_Token{
								Token: authToken,
							},
						}},
					}},
				},
			},
		}},
	})
	template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
	coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
	workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
	coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

	for _, res := range workspace.LatestBuild.Resources {
		for _, a := range res.Agents {
			require.Equal(t, codersdk.WorkspaceAgentLifecycleCreated, a.LifecycleState)
		}
	}

	agentClient := agentsdk.New(client.URL)
	agentClient.SetSessionToken(authToken)

	ctx := testutil.Context(t, testutil.WaitMedium)

	manifest, err := agentClient.Manifest(ctx)
	require.NoError(t, err)

	// Verify manifest API response.
	require.Equal(t, "First Meta", manifest.Metadata[0].DisplayName)
	require.Equal(t, "foo1", manifest.Metadata[0].Key)
	require.Equal(t, "echo hi", manifest.Metadata[0].Script)
	require.EqualValues(t, 10, manifest.Metadata[0].Interval)
	require.EqualValues(t, 3, manifest.Metadata[0].Timeout)

	post := func(key string, mr codersdk.WorkspaceAgentMetadataResult) {
		err := agentClient.PostMetadata(ctx, key, mr)
		require.NoError(t, err, "post metadata", t)
	}

	workspace, err = client.Workspace(ctx, workspace.ID)
	require.NoError(t, err, "get workspace")

	agentID := workspace.LatestBuild.Resources[0].Agents[0].ID

	var update []codersdk.WorkspaceAgentMetadata

	wantMetadata1 := codersdk.WorkspaceAgentMetadataResult{
		CollectedAt: time.Now(),
		Value:       "bar",
	}

	// Initial post must come before the Watch is established.
	post("foo1", wantMetadata1)

	updates, errors := client.WatchWorkspaceAgentMetadata(ctx, agentID)

	recvUpdate := func() []codersdk.WorkspaceAgentMetadata {
		select {
		case <-ctx.Done():
			t.Fatalf("context done: %v", ctx.Err())
		case err := <-errors:
			t.Fatalf("error watching metadata: %v", err)
		case update := <-updates:
			return update
		}
		return nil
	}

	check := func(want codersdk.WorkspaceAgentMetadataResult, got codersdk.WorkspaceAgentMetadata, retry bool) {
		// We can't trust the order of the updates due to timers and debounces,
		// so let's check a few times more.
		for i := 0; retry && i < 2 && (want.Value != got.Result.Value || want.Error != got.Result.Error); i++ {
			update = recvUpdate()
			for _, m := range update {
				if m.Description.Key == got.Description.Key {
					got = m
					break
				}
			}
		}
		ok1 := assert.Equal(t, want.Value, got.Result.Value)
		ok2 := assert.Equal(t, want.Error, got.Result.Error)
		if !ok1 || !ok2 {
			require.FailNow(t, "check failed")
		}
	}

	update = recvUpdate()
	require.Len(t, update, 3)
	check(wantMetadata1, update[0], false)
	// The second metadata result is not yet posted.
	require.Zero(t, update[1].Result.CollectedAt)

	wantMetadata2 := wantMetadata1
	post("foo2", wantMetadata2)
	update = recvUpdate()
	require.Len(t, update, 3)
	check(wantMetadata1, update[0], true)
	check(wantMetadata2, update[1], true)

	wantMetadata1.Error = "error"
	post("foo1", wantMetadata1)
	update = recvUpdate()
	require.Len(t, update, 3)
	check(wantMetadata1, update[0], true)

	const maxValueLen = 32 << 10
	tooLongValueMetadata := wantMetadata1
	tooLongValueMetadata.Value = strings.Repeat("a", maxValueLen*2)
	tooLongValueMetadata.Error = ""
	tooLongValueMetadata.CollectedAt = time.Now()
	post("foo3", tooLongValueMetadata)
	got := recvUpdate()[2]
	for i := 0; i < 2 && len(got.Result.Value) != maxValueLen; i++ {
		got = recvUpdate()[2]
	}
	require.Len(t, got.Result.Value, maxValueLen)
	require.NotEmpty(t, got.Result.Error)

	unknownKeyMetadata := wantMetadata1
	err = agentClient.PostMetadata(ctx, "unknown", unknownKeyMetadata)
	require.NoError(t, err)
}

func TestWorkspaceAgent_Startup(t *testing.T) {
	t.Parallel()

	t.Run("OK", func(t *testing.T) {
		t.Parallel()

		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		ctx := testutil.Context(t, testutil.WaitMedium)

		const (
			expectedVersion   = "v1.2.3"
			expectedDir       = "/home/coder"
			expectedSubsystem = codersdk.AgentSubsystemEnvbox
		)

		err := agentClient.PostStartup(ctx, agentsdk.PostStartupRequest{
			Version:           expectedVersion,
			ExpandedDirectory: expectedDir,
			Subsystem:         expectedSubsystem,
		})
		require.NoError(t, err)

		workspace, err = client.Workspace(ctx, workspace.ID)
		require.NoError(t, err)

		wsagent, err := client.WorkspaceAgent(ctx, workspace.LatestBuild.Resources[0].Agents[0].ID)
		require.NoError(t, err)
		require.Equal(t, expectedVersion, wsagent.Version)
		require.Equal(t, expectedDir, wsagent.ExpandedDirectory)
		require.Equal(t, expectedSubsystem, wsagent.Subsystem)
	})

	t.Run("InvalidSemver", func(t *testing.T) {
		t.Parallel()

		client := coderdtest.New(t, &coderdtest.Options{
			IncludeProvisionerDaemon: true,
		})
		user := coderdtest.CreateFirstUser(t, client)
		authToken := uuid.NewString()
		version := coderdtest.CreateTemplateVersion(t, client, user.OrganizationID, &echo.Responses{
			Parse:          echo.ParseComplete,
			ProvisionPlan:  echo.ProvisionComplete,
			ProvisionApply: echo.ProvisionApplyWithAgent(authToken),
		})
		template := coderdtest.CreateTemplate(t, client, user.OrganizationID, version.ID)
		coderdtest.AwaitTemplateVersionJob(t, client, version.ID)
		workspace := coderdtest.CreateWorkspace(t, client, user.OrganizationID, template.ID)
		coderdtest.AwaitWorkspaceBuildJob(t, client, workspace.LatestBuild.ID)

		agentClient := agentsdk.New(client.URL)
		agentClient.SetSessionToken(authToken)

		ctx := testutil.Context(t, testutil.WaitMedium)

		err := agentClient.PostStartup(ctx, agentsdk.PostStartupRequest{
			Version: "1.2.3",
		})
		require.Error(t, err)
		cerr, ok := codersdk.AsError(err)
		require.True(t, ok)
		require.Equal(t, http.StatusBadRequest, cerr.StatusCode())
	})
}
