package main

import (
	"errors"
	"fmt"
	"net"
	"strings"

	proxy "github.com/fabianvf/owner-ref-proxy/proxy"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	cmdutil "k8s.io/kubernetes/pkg/kubectl/cmd/util"

	"github.com/golang/glog"
	"github.com/spf13/cobra"
	"k8s.io/kubernetes/pkg/kubectl/cmd/templates"
	"k8s.io/kubernetes/pkg/kubectl/util/i18n"
)

var (
	defaultPort = 8001
	proxyLong   = templates.LongDesc(i18n.T(`
		Creates a proxy server or application-level gateway between localhost and 
		the Kubernetes API Server. It also allows serving static content over specified 
		HTTP path. All incoming data enters through one port and gets forwarded to 
		the remote kubernetes API Server port, except for the path matching the static content path.`))

	proxyExample = templates.Examples(i18n.T(`
		# To proxy all of the kubernetes api and nothing else, use:

		    $ kubectl proxy --api-prefix=/

		# To proxy only part of the kubernetes api and also some static files:

		    $ kubectl proxy --www=/my/files --www-prefix=/static/ --api-prefix=/api/

		# The above lets you 'curl localhost:8001/api/v1/pods'.

		# To proxy the entire kubernetes api at a different root, use:

		    $ kubectl proxy --api-prefix=/custom/

		# The above lets you 'curl localhost:8001/custom/api/v1/pods'

		# Run a proxy to kubernetes apiserver on port 8011, serving static content from ./local/www/
		kubectl proxy --port=8011 --www=./local/www/

		# Run a proxy to kubernetes apiserver on an arbitrary local port.
		# The chosen port for the server will be output to stdout.
		kubectl proxy --port=0

		# Run a proxy to kubernetes apiserver, changing the api prefix to k8s-api
		# This makes e.g. the pods api available at localhost:8001/k8s-api/v1/pods/
		kubectl proxy --api-prefix=/k8s-api`))
)

func NewCmdProxy(f cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use: "proxy [--port=PORT] [--api-prefix=prefix] [--owner-uid=uid]",
		DisableFlagsInUseLine: true,
		Short:   i18n.T("Run a proxy to the Kubernetes API server"),
		Long:    proxyLong,
		Example: proxyExample,
		Run: func(cmd *cobra.Command, args []string) {
			err := RunProxy(f, cmd)
			cmdutil.CheckErr(err)
		},
	}
	cmd.Flags().StringP("api-prefix", "", "/", "Prefix to serve the proxied API under.")
	cmd.Flags().StringP("owner-uid", "i", "", "UID of the owner of objects created through this proxy.")
	cmd.Flags().StringP("owner-kind", "k", "", "kind of the owner of objects created through this proxy.")
	cmd.Flags().StringP("owner-name", "n", "", "name of the owner of objects created through this proxy.")
	cmd.Flags().StringP("owner-api-version", "v", "", "APIVersion of the owner of objects created through this proxy.")
	cmd.Flags().String("accept-paths", proxy.DefaultPathAcceptRE, "Regular expression for paths that the proxy should accept.")
	cmd.Flags().String("reject-paths", proxy.DefaultPathRejectRE, "Regular expression for paths that the proxy should reject. Paths specified here will be rejected even accepted by --accept-paths.")
	cmd.Flags().String("accept-hosts", proxy.DefaultHostAcceptRE, "Regular expression for hosts that the proxy should accept.")
	cmd.Flags().String("reject-methods", proxy.DefaultMethodRejectRE, "Regular expression for HTTP methods that the proxy should reject (example --reject-methods='POST,PUT,PATCH'). ")
	cmd.Flags().IntP("port", "p", defaultPort, "The port on which to run the proxy. Set to 0 to pick a random port.")
	cmd.Flags().StringP("address", "", "127.0.0.1", "The IP address on which to serve on.")
	cmd.Flags().StringP("unix-socket", "u", "", "Unix socket on which to run the proxy.")
	return cmd
}

func RunProxy(f cmdutil.Factory, cmd *cobra.Command) error {
	path := cmdutil.GetFlagString(cmd, "unix-socket")
	port := cmdutil.GetFlagInt(cmd, "port")
	address := cmdutil.GetFlagString(cmd, "address")

	if port != defaultPort && path != "" {
		return errors.New("Don't specify both --unix-socket and --port")
	}

	clientConfig, err := f.ClientConfig()
	if err != nil {
		return err
	}

	apiProxyPrefix := cmdutil.GetFlagString(cmd, "api-prefix")
	if !strings.HasSuffix(apiProxyPrefix, "/") {
		apiProxyPrefix += "/"
	}
	filter := &proxy.FilterServer{
		AcceptPaths:   proxy.MakeRegexpArrayOrDie(cmdutil.GetFlagString(cmd, "accept-paths")),
		RejectPaths:   proxy.MakeRegexpArrayOrDie(cmdutil.GetFlagString(cmd, "reject-paths")),
		AcceptHosts:   proxy.MakeRegexpArrayOrDie(cmdutil.GetFlagString(cmd, "accept-hosts")),
		RejectMethods: proxy.MakeRegexpArrayOrDie(cmdutil.GetFlagString(cmd, "reject-methods")),
	}

	reference := metav1.OwnerReference{
		APIVersion: cmdutil.GetFlagString(cmd, "owner-api-version"),
		Kind:       cmdutil.GetFlagString(cmd, "owner-kind"),
		Name:       cmdutil.GetFlagString(cmd, "owner-name"),
		UID:        types.UID(cmdutil.GetFlagString(cmd, "owner-uid")),
	}
	if reference.APIVersion == "" || reference.Kind == "" || reference.Name == "" || reference.UID == "" {
		glog.Fatalf("You must provide APIVersion, kind, name and UID for the owner")
	}

	server, err := proxy.NewServer(apiProxyPrefix, filter, clientConfig, reference)

	// Separate listening from serving so we can report the bound port
	// when it is chosen by os (eg: port == 0)
	var l net.Listener
	if path == "" {
		l, err = server.Listen(address, port)
	} else {
		l, err = server.ListenUnix(path)
	}
	if err != nil {
		glog.Fatal(err)
	}
	fmt.Printf("Starting to serve on %s\n", l.Addr().String())
	glog.Fatal(server.ServeOnListener(l))
	return nil
}
func main() {
	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{})
	NewCmdProxy(cmdutil.NewFactory(config)).Execute()
}
