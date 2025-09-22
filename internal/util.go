package internal

import (
	"context"
	"errors"
	"fmt"
	ioFs "io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/antihax/optional"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"github.com/crusoecloud/crusoe-csi-driver/internal/crusoe"
	"github.com/google/uuid"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	projectIDEnvKey   = "CRUSOE_PROJECT_ID"
	projectIDLabelKey = "crusoe.ai/project.id"

	vmIDFilePath = "/sys/class/dmi/id/product_uuid"

	gracefulTimeoutDuration = 10 * time.Second
)

var (
	errInstanceNotFound  = errors.New("instance not found")
	errMultipleInstances = errors.New("multiple instances found")
	errVMIDReadFailed    = fmt.Errorf("failed to read %s for VM ID", vmIDFilePath)
	errVMIDParseFailed   = fmt.Errorf("failed to parse %s for VM ID", vmIDFilePath)
	errProjectIDNotFound = fmt.Errorf("project ID not found in %s env var or %s node label",
		projectIDEnvKey, projectIDLabelKey)
)

//nolint:cyclop // function is already fairly clean
func getHostInstance(ctx context.Context) (*crusoeapi.InstanceV1Alpha5, error) {
	crusoeClient := crusoe.NewCrusoeClient(
		viper.GetString(CrusoeAPIEndpointFlag),
		viper.GetString(CrusoeAccessKeyFlag),
		viper.GetString(CrusoeSecretKeyFlag),
		fmt.Sprintf("%s/%s", common.PluginName, common.PluginVersion),
	)

	vmIDStringByteArray, err := os.ReadFile(vmIDFilePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errVMIDReadFailed, err)
	}

	vmIDString := strings.TrimSpace(string(vmIDStringByteArray))
	_, err = uuid.Parse(vmIDString)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errVMIDParseFailed, err)
	}

	var projectID string

	projectID = viper.GetString(CrusoeProjectIDFlag)
	if projectID == "" {
		var ok bool
		kubeClientConfig, configErr := rest.InClusterConfig()
		if configErr != nil {
			return nil, fmt.Errorf("could not get kube client config: %w", configErr)
		}

		kubeClient, clientErr := kubernetes.NewForConfig(kubeClientConfig)
		if clientErr != nil {
			return nil, fmt.Errorf("could not get kube client: %w", clientErr)
		}
		hostNode, nodeFetchErr := kubeClient.CoreV1().Nodes().Get(ctx, viper.GetString(NodeNameFlag), metav1.GetOptions{})
		if nodeFetchErr != nil {
			return nil, fmt.Errorf("could not fetch current node with kube client: %w", nodeFetchErr)
		}

		projectID, ok = hostNode.Labels[projectIDLabelKey]
		if !ok {
			return nil, errProjectIDNotFound
		}
	}

	instances, _, err := crusoeClient.VMsApi.ListInstances(ctx, projectID,
		&crusoeapi.VMsApiListInstancesOpts{
			Ids: optional.NewString(vmIDString),
		})
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}

	if len(instances.Items) == 0 {
		return nil, fmt.Errorf("%w: %s", errInstanceNotFound, vmIDString)
	} else if len(instances.Items) > 1 {
		return nil, fmt.Errorf("%w: %s", errMultipleInstances, vmIDString)
	}

	return &instances.Items[0], nil
}

func listen() (net.Listener, error) {
	ep, err := url.Parse(viper.GetString(SocketAddressFlag))
	if err != nil {
		return nil, fmt.Errorf("failed to parse socket url: %w", err)
	}

	if ep.Scheme == "unix" {
		removeErr := os.Remove(ep.Path)
		if removeErr != nil {
			if !errors.Is(removeErr, ioFs.ErrNotExist) {
				return nil, fmt.Errorf("failed to remove socket file %s: %w", ep.Path, removeErr)
			}
		}
	}
	listener, listenErr := net.Listen(ep.Scheme, ep.Path)
	if listenErr != nil {
		return nil, fmt.Errorf("failed to start listener on provided socket url: %w", listenErr)
	}

	return listener, nil
}

func gracefulStopWithTimeout(grpcServer *grpc.Server, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	doneCh := make(chan struct{}, 1)

	go func() {
		grpcServer.GracefulStop()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		break
	case <-ctx.Done():
		klog.Infof("Graceful stop timeout exceeded, forcing stop")
		grpcServer.Stop()
	}
}

func newCrusoeClientWithViperConfig() *crusoeapi.APIClient {
	return crusoe.NewCrusoeClient(
		viper.GetString(CrusoeAPIEndpointFlag),
		viper.GetString(CrusoeAccessKeyFlag),
		viper.GetString(CrusoeSecretKeyFlag),
		common.GetUserAgent(),
	)
}

func newCrusoeHTTPClientWithViperConfig() *http.Client {
	return crusoe.NewCrusoeHTTPClient(viper.GetString(CrusoeAccessKeyFlag), viper.GetString(CrusoeSecretKeyFlag))
}
