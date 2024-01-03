/*
Copyright 2023 The Kubernetes authors.

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
// +kubebuilder:docs-gen:collapse=Apache License

package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/vishvananda/netlink"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/event"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	batchv1 "tutorial.kubebuilder.io/project/api/v1"
	"tutorial.kubebuilder.io/project/internal/controller"
	//+kubebuilder:scaffold:imports
)

// +kubebuilder:docs-gen:collapse=Imports

/*
The first difference to notice is that kubebuilder has added the new API
group's package (`batchv1`) to our scheme.  This means that we can use those
objects in our controller.

If we would be using any other CRD we would have to add their scheme the same way.
Builtin types such as Job have their scheme added by `clientgoscheme`.
*/

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(batchv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

/*
The other thing that's changed is that kubebuilder has added a block calling our
CronJob controller's `SetupWithManager` method.
*/

func main() {
	// Create channels to receive notifications for link, address, and route changes
	chLink := make(chan netlink.LinkUpdate)
	doneLink := make(chan struct{})
	defer close(doneLink)

	chAddr := make(chan netlink.AddrUpdate)
	doneAddr := make(chan struct{})
	defer close(doneAddr)

	chRoute := make(chan netlink.RouteUpdate)
	doneRoute := make(chan struct{})
	defer close(doneRoute)

	c := make(chan event.GenericEvent)

	// Subscribe to the address updates
	if err := netlink.AddrSubscribe(chAddr, doneAddr); err != nil {
		fmt.Println("Error:", err)
		return
	}

	// Subscribe to the link updates
	if err := netlink.LinkSubscribe(chLink, doneLink); err != nil {
		fmt.Println("Error:", err)
		return
	}

	// Subscribe to the route updates
	if err := netlink.RouteSubscribe(chRoute, doneRoute); err != nil {
		fmt.Println("Error:", err)
		return
	}

	// Create maps to keep track of interfaces and newly created interfaces
	newlyCreated := make(map[string]bool)
	interfaces := make(map[string]bool)

	// Get the list of existing links and add them to the interfaces map
	links, err := netlink.LinkList()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	for _, link := range links {
		interfaces[link.Attrs().Name] = true
	}

	fmt.Println("Monitoring started. Press Ctrl+C to stop it.")

	// Start an infinite loop to handle the notifications
	/************************* GO ROUTINE *******************************/
	// TOASK: is it ok?
	go func() {
		for {
			select {
			case updateLink := <-chLink:
				handleLinkUpdate(updateLink, interfaces, newlyCreated, c)
			case updateAddr := <-chAddr:
				handleAddrUpdate(updateAddr, interfaces, c)
			case updateRoute := <-chRoute:
				handleRouteUpdate(updateRoute, c)
			}
		}
	}()

	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	// TOASK: options? Default namespace?
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "80807133.tutorial.kubebuilder.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// +kubebuilder:docs-gen:collapse=old stuff

	if err = (&controller.CronJobReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr, c); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CronJob")
		os.Exit(1)
	}
}

func handleLinkUpdate(updateLink netlink.LinkUpdate, interfaces map[string]bool, newlyCreated map[string]bool, c chan<- event.GenericEvent) {
	if updateLink.Header.Type == syscall.RTM_DELLINK {
		// Link has been removed
		fmt.Println("Interface removed:", updateLink.Link.Attrs().Name)
		delete(interfaces, updateLink.Link.Attrs().Name)
		delete(newlyCreated, updateLink.Link.Attrs().Name)
	} else if !interfaces[updateLink.Link.Attrs().Name] && updateLink.Header.Type == syscall.RTM_NEWLINK {
		// New link has been added
		fmt.Println("Interface added")
		interfaces[updateLink.Link.Attrs().Name] = true
		newlyCreated[updateLink.Link.Attrs().Name] = true
	} else if updateLink.Header.Type == syscall.RTM_NEWLINK {
		// Link has been modified
		if updateLink.Link.Attrs().Flags&net.FlagUp != 0 {
			fmt.Println("Interface", updateLink.Link.Attrs().Name, "is up")
			delete(newlyCreated, updateLink.Link.Attrs().Name)
		} else if !newlyCreated[updateLink.Link.Attrs().Name] {
			fmt.Println("Interface", updateLink.Link.Attrs().Name, "is down")
		}
	}
	send(c)
}

func handleAddrUpdate(updateAddr netlink.AddrUpdate, interfaces map[string]bool, c chan<- event.GenericEvent) {
	iface, err := net.InterfaceByIndex(updateAddr.LinkIndex)
	if err != nil {
		fmt.Println("Address (", updateAddr.LinkAddress.IP, ") removed from the deleted interface")
		return
	}
	if updateAddr.NewAddr {
		// New address has been added
		fmt.Println("New address (", updateAddr.LinkAddress.IP, ") added to the interface:", iface.Name)
	} else {
		// Address has been removed
		fmt.Println("Address (", updateAddr.LinkAddress.IP, ") removed from the interface:", iface.Name)
	}
	send(c)
}

func handleRouteUpdate(updateRoute netlink.RouteUpdate, c chan<- event.GenericEvent) {
	if updateRoute.Type == syscall.RTM_NEWROUTE {
		// New route has been added
		fmt.Println("New route added:", updateRoute.Route.Dst)
	} else if updateRoute.Type == syscall.RTM_DELROUTE {
		// Route has been removed
		fmt.Println("Route removed:", updateRoute.Route.Dst)
	}
	send(c)
}

// send a channel with generic event type
func send(c chan<- event.GenericEvent) {
	ge := event.GenericEvent{}
	c <- ge
	klog.Infof("Starting Netlink routine")
}
