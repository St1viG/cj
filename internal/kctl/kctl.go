// Package kctl is the Kondukter CLI (ADR-0005): the customer, partner and
// operator client of the public Connect API and the demo's driver.
package kctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/St1viG/cj/gen/go/kondukter/api/v1/apiv1connect"
	"github.com/St1viG/cj/internal/version"
)

// Config is ~/.config/kctl/config.yaml.
type Config struct {
	Server string `yaml:"server"`
	APIKey string `yaml:"api_key"`
}

// DefaultConfigPath is $XDG_CONFIG_HOME/kctl/config.yaml, else
// ~/.config/kctl/config.yaml.
func DefaultConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "kctl", "config.yaml")
}

func loadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path) //nolint:gosec // the user's own config file
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, yaml.Unmarshal(b, &c)
}

func saveConfig(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(c) //nolint:gosec // G117: storing the key is the point; the file is 0600
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// app is the state shared by every command.
type app struct {
	configPath string
	server     string
	apiKey     string
	output     string
	httpClient *http.Client
	out, err   io.Writer
	in         io.Reader
}

// Options configure New; zero values pick the defaults.
type Options struct {
	HTTPClient *http.Client
	Out, Err   io.Writer
	In         io.Reader
}

// New returns the kctl root command.
func New(o Options) *cobra.Command {
	a := &app{httpClient: o.HTTPClient, out: o.Out, err: o.Err, in: o.In}
	if a.httpClient == nil {
		a.httpClient = http.DefaultClient
	}
	if a.out == nil {
		a.out = os.Stdout
	}
	if a.err == nil {
		a.err = os.Stderr
	}
	if a.in == nil {
		a.in = os.Stdin
	}
	root := &cobra.Command{
		Use:           "kctl",
		Short:         "Kondukter CLI: offers, instances, nodes and usage",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String("kctl"),
	}
	root.SetOut(a.out)
	root.SetErr(a.err)
	root.SetIn(a.in)
	pf := root.PersistentFlags()
	pf.StringVar(&a.configPath, "config", envOr("KCTL_CONFIG", DefaultConfigPath()), "config file")
	pf.StringVar(&a.server, "server", os.Getenv("KCTL_SERVER"), "API URL (default: from the config file)")
	pf.StringVar(&a.apiKey, "api-key", os.Getenv("KCTL_API_KEY"), "API key (default: from the config file)")
	pf.StringVarP(&a.output, "output", "o", "table", "output format: table or json")
	root.AddCommand(
		a.loginCmd(), a.whoamiCmd(), a.offersCmd(), a.imagesCmd(), a.createCmd(), a.getCmd(), a.listCmd(),
		a.watchCmd(), a.terminateCmd(), a.sshCmd(), a.nodesCmd(), a.drainCmd(), a.undrainCmd(), a.enrollTokenCmd(), a.usageCmd(),
	)
	return root
}

// Run executes kctl with args and returns the process exit code. Errors
// print as one line: "kctl: <connect code>: <message>".
func Run(ctx context.Context, args []string, o Options) int {
	cmd := New(o)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(ctx); err != nil {
		w := o.Err
		if w == nil {
			w = os.Stderr
		}
		fmt.Fprintln(w, "kctl: "+oneLine(err))
		var ex exitError
		if errors.As(err, &ex) {
			return ex.code
		}
		return 1
	}
	return 0
}

// exitError carries a non-default exit code (watch ending in FAILED).
type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

func oneLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// resolved returns the server and API key from flags, env, then config.
func (a *app) resolved() (Config, error) {
	c, err := loadConfig(a.configPath)
	if err != nil {
		return c, fmt.Errorf("config %s: %w", a.configPath, err)
	}
	if a.server != "" {
		c.Server = a.server
	}
	if a.apiKey != "" {
		c.APIKey = a.apiKey
	}
	c.Server = strings.TrimRight(c.Server, "/")
	switch {
	case c.Server == "":
		return c, errors.New("no server: run `kctl login --server URL` or pass --server")
	case c.APIKey == "":
		return c, errors.New("no API key: run `kctl login` or pass --api-key")
	}
	return c, nil
}

// bearer adds the API key to every request.
type bearer string

func (b bearer) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		req.Header().Set("Authorization", "Bearer "+string(b))
		return next(ctx, req)
	}
}

func (b bearer) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		conn.RequestHeader().Set("Authorization", "Bearer "+string(b))
		return conn
	}
}

func (b bearer) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// clients are the API clients for one invocation.
type clients struct {
	catalog   apiv1connect.CatalogServiceClient
	instances apiv1connect.InstanceServiceClient
	auth      apiv1connect.AuthServiceClient
	partner   apiv1connect.PartnerServiceClient
	usage     apiv1connect.UsageServiceClient
}

func (a *app) connect(c Config) clients {
	opts := connect.WithInterceptors(bearer(c.APIKey))
	return clients{
		catalog:   apiv1connect.NewCatalogServiceClient(a.httpClient, c.Server, opts),
		instances: apiv1connect.NewInstanceServiceClient(a.httpClient, c.Server, opts),
		auth:      apiv1connect.NewAuthServiceClient(a.httpClient, c.Server, opts),
		partner:   apiv1connect.NewPartnerServiceClient(a.httpClient, c.Server, opts),
		usage:     apiv1connect.NewUsageServiceClient(a.httpClient, c.Server, opts),
	}
}

func (a *app) clients() (clients, error) {
	c, err := a.resolved()
	if err != nil {
		return clients{}, err
	}
	return a.connect(c), nil
}

func (a *app) json() (bool, error) {
	switch a.output {
	case "json":
		return true, nil
	case "table", "":
		return false, nil
	}
	return false, fmt.Errorf("--output %q: want table or json", a.output)
}

// emit prints m as one line of JSON (camelCase, defaults included).
func (a *app) emit(m proto.Message) error {
	b, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(m)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.out, string(b))
	return err
}

// table prints rows aligned under header.
func (a *app) table(header string, rows [][]string) error {
	tw := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, header)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	return tw.Flush()
}

// output prints m as JSON or calls tableFn.
func (a *app) print(m proto.Message, tableFn func() error) error {
	j, err := a.json()
	if err != nil {
		return err
	}
	if j {
		return a.emit(m)
	}
	return tableFn()
}
