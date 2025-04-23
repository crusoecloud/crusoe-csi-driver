package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/crusoecloud/crusoe-csi-driver/internal"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"k8s.io/klog/v2"
)

//nolint:gochecknoglobals  // Global command instance
var rootCmd = &cobra.Command{
	Use:          "health-probe",
	Short:        "Crusoe Container Storage Interface (CSI) health probe utility",
	SilenceUsage: true, // Silence usage print if an error occurs
	RunE:         HealthProbe,
}

const (
	probeTimeout = 10 * time.Second
)

func ProbeHelper(ctx context.Context) error {
	// Set up connection options
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	// Connect to the socket using NewClient
	conn, err := grpc.NewClient(viper.GetString(internal.SocketAddressFlag), opts...)
	if err != nil {
		klog.Errorf("Failed to connect to CSI socket")

		return fmt.Errorf("failed to connect to CSI socket: %w", err)
	}
	defer func(conn *grpc.ClientConn) {
		closeErr := conn.Close()
		if closeErr != nil {
			klog.Errorf("Failed to close connection: %v\n", err)
		}
	}(conn)

	// Create the identity client
	identityClient := csi.NewIdentityClient(conn)

	// Example: Call GetPluginInfo method
	_, err = identityClient.Probe(ctx, &csi.ProbeRequest{})
	if err != nil {
		klog.Errorf("Failed to probe CSI service: %v\n", err)

		return fmt.Errorf("failed to probe CSI service: %w", err)
	}

	return nil
}

func HealthProbe(_ *cobra.Command, _ []string) error {
	// Set up a context with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	err := ProbeHelper(ctx)
	if err != nil {
		klog.Errorf("Health probe failed: %v\n", err)

		return err
	}

	klog.Infof("Health probe successful")

	return nil
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func setFlags() {
	var err error
	viper.AutomaticEnv()

	// Use underscores in env var names
	replacer := strings.NewReplacer("-", "_")
	viper.SetEnvKeyReplacer(replacer)

	rootCmd.Flags().String(internal.SocketAddressFlag, internal.SocketAddressDefault, "CSI Socket Address")

	err = viper.BindPFlags(rootCmd.Flags())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	setFlags()
	Execute()
}
