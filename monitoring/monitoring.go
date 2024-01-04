package monitoring

import (
	"fmt"
	"net"
	"syscall"

	"github.com/vishvananda/netlink"
	"sigs.k8s.io/controller-runtime/pkg/event"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	//+kubebuilder:scaffold:imports
)

func startMonitoring(c chan event.GenericEvent) {
	// TODO handle different types of callings (just interface, just address...)
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

	// Start an infinite loop to handle the notifications
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
	iface, err := net.InterfaceByIndex(updateAddr.LinkIndex) // Change to pass the error to the caller
	if err != nil {
		fmt.Println("Address (", updateAddr.LinkAddress.IP, ") removed from the deleted interface")
		return
	}
	if updateAddr.NewAddr {
		// New address has been added
		fmt.Println("New address (", updateAddr.LinkAddress.IP, ") added to the interface:", iface.Name)
		send(c)
	} else {
		// Address has been removed
		fmt.Println("Address (", updateAddr.LinkAddress.IP, ") removed from the interface:", iface.Name)
		send(c)
	}
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
	// Triggers a new reconcile
	ge := event.GenericEvent{}
	c <- ge
}
