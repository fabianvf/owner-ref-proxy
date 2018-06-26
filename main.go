/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/golang/glog"
	"github.com/spf13/cobra"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	utilproxy "k8s.io/apimachinery/pkg/util/proxy"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
	"k8s.io/kubernetes/pkg/kubectl/cmd/templates"
	cmdutil "k8s.io/kubernetes/pkg/kubectl/cmd/util"
	"k8s.io/kubernetes/pkg/kubectl/proxy"
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

type responder struct{}

func (r *responder) Error(w http.ResponseWriter, req *http.Request, err error) {
	glog.Errorf("Error while proxying request: %v", err)
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func NewCmdProxy(f cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use: "proxy [--port=PORT] [--www=static-dir] [--www-prefix=prefix] [--api-prefix=prefix]",
		DisableFlagsInUseLine: true,
		Short:   i18n.T("Run a proxy to the Kubernetes API server"),
		Long:    proxyLong,
		Example: proxyExample,
		Run: func(cmd *cobra.Command, args []string) {
			err := RunProxy(f, cmd)
			cmdutil.CheckErr(err)
		},
	}
	cmd.Flags().StringP("www", "w", "", "Also serve static files from the given directory under the specified prefix.")
	cmd.Flags().StringP("www-prefix", "P", "/static/", "Prefix to serve static files under, if static file directory is specified.")
	cmd.Flags().StringP("api-prefix", "", "/", "Prefix to serve the proxied API under.")
	cmd.Flags().String("accept-paths", proxy.DefaultPathAcceptRE, "Regular expression for paths that the proxy should accept.")
	cmd.Flags().String("reject-paths", proxy.DefaultPathRejectRE, "Regular expression for paths that the proxy should reject. Paths specified here will be rejected even accepted by --accept-paths.")
	cmd.Flags().String("accept-hosts", proxy.DefaultHostAcceptRE, "Regular expression for hosts that the proxy should accept.")
	cmd.Flags().String("reject-methods", proxy.DefaultMethodRejectRE, "Regular expression for HTTP methods that the proxy should reject (example --reject-methods='POST,PUT,PATCH'). ")
	cmd.Flags().IntP("port", "p", defaultPort, "The port on which to run the proxy. Set to 0 to pick a random port.")
	cmd.Flags().StringP("address", "", "127.0.0.1", "The IP address on which to serve on.")
	cmd.Flags().Bool("disable-filter", false, "If true, disable request filtering in the proxy. This is dangerous, and can leave you vulnerable to XSRF attacks, when used with an accessible port.")
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

	staticPrefix := cmdutil.GetFlagString(cmd, "www-prefix")
	if !strings.HasSuffix(staticPrefix, "/") {
		staticPrefix += "/"
	}
	staticDir := cmdutil.GetFlagString(cmd, "www")
	if staticDir != "" {
		fileInfo, err := os.Stat(staticDir)
		if err != nil {
			glog.Warning("Failed to stat static file directory "+staticDir+": ", err)
		} else if !fileInfo.IsDir() {
			glog.Warning("Static file directory " + staticDir + " is not a directory")
		}
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
	if cmdutil.GetFlagBool(cmd, "disable-filter") {
		if path == "" {
			glog.Warning("Request filter disabled, your proxy is vulnerable to XSRF attacks, please be cautious")
		}
		filter = nil
	}

	server, err := NewServer(staticDir, apiProxyPrefix, staticPrefix, filter, clientConfig)

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

// NewServer creates and installs a new Server.
// 'filter', if non-nil, protects requests to the api only.
func NewServer(filebase string, apiProxyPrefix string, staticPrefix string, filter *proxy.FilterServer, cfg *rest.Config) (*proxy.Server, error) {
	host := cfg.Host
	if !strings.HasSuffix(host, "/") {
		host = host + "/"
	}
	target, err := url.Parse(host)
	if err != nil {
		return nil, err
	}

	responder := &responder{}
	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, err
	}
	upgradeTransport, err := makeUpgradeTransport(cfg)
	if err != nil {
		return nil, err
	}
	proxyInst := utilproxy.NewUpgradeAwareHandler(target, transport, false, false, responder)
	proxyInst.UpgradeTransport = upgradeTransport
	proxyInst.UseRequestLocation = true

	proxyServer := http.Handler(proxyInst)
	if filter != nil {
		proxyServer = filter.HandlerFor(proxyServer)
	}

	if !strings.HasPrefix(apiProxyPrefix, "/api") {
		proxyServer = stripLeaveSlash(apiProxyPrefix, proxyServer)
	}

	mux := http.NewServeMux()
	mux.Handle(apiProxyPrefix, proxyServer)
	if filebase != "" {
		// Require user to explicitly request this behavior rather than
		// serving their working directory by default.
		mux.Handle(staticPrefix, newFileHandler(staticPrefix, filebase))
	}
	return &proxy.Server{handler: mux}, nil
}

// makeUpgradeTransport creates a transport that explicitly bypasses HTTP2 support
// for proxy connections that must upgrade.
func makeUpgradeTransport(config *rest.Config) (utilproxy.UpgradeRequestRoundTripper, error) {
	transportConfig, err := config.TransportConfig()
	if err != nil {
		return nil, err
	}
	tlsConfig, err := transport.TLSConfigFor(transportConfig)
	if err != nil {
		return nil, err
	}
	rt := utilnet.SetOldTransportDefaults(&http.Transport{
		TLSClientConfig: tlsConfig,
	})
	upgrader, err := transport.HTTPWrappersForConfig(transportConfig, utilproxy.MirrorRequest)
	if err != nil {
		return nil, err
	}
	return utilproxy.NewUpgradeRequestRoundTripper(rt, upgrader), nil
}

// like http.StripPrefix, but always leaves an initial slash. (so that our
// regexps will work.)
func stripLeaveSlash(prefix string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := strings.TrimPrefix(req.URL.Path, prefix)
		if len(p) >= len(req.URL.Path) {
			http.NotFound(w, req)
			return
		}
		if len(p) > 0 && p[:1] != "/" {
			p = "/" + p
		}
		req.URL.Path = p
		h.ServeHTTP(w, req)
	})
}

func newFileHandler(prefix, base string) http.Handler {
	return http.StripPrefix(prefix, http.FileServer(http.Dir(base)))
}

func main() {
	NewCmdProxy(cmdutil.NewFactory(nil)).Execute()
}
