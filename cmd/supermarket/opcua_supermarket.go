package main

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
)

var (
	endpoint = flag.String("endpoint", "0.0.0.0", "OPC UA Endpoint URL")
	port     = flag.Int("port", 4840, "OPC UA Endpoint port")
	certfile = flag.String("cert", "cert.pem", "Path to certificate file")
	keyfile  = flag.String("key", "key.pem", "Path to PEM Private Key file")
	gencert  = flag.Bool("gen-cert", false, "Generate a new certificate")
)

const (
	COUNT_FLAGS = 4000
	COUNT_INT   = 2000
	COUNT_REAL  = 1000
)

type Logger int

func (l Logger) Debug(msg string, args ...any) {}
func (l Logger) Info(msg string, args ...any)  { log.Printf(msg, args...) }
func (l Logger) Warn(msg string, args ...any)  { log.Printf(msg, args...) }
func (l Logger) Error(msg string, args ...any) { log.Printf(msg, args...) }

type DataSupermarket struct {
	mu sync.RWMutex

	Flags      [COUNT_FLAGS]bool
	IntValues  [COUNT_INT]int32
	RealValues [COUNT_REAL]float64

	FlagDV [COUNT_FLAGS]ua.DataValue
	IntDV  [COUNT_INT]ua.DataValue
	RealDV [COUNT_REAL]ua.DataValue
}

func NewDataSupermarket() *DataSupermarket {
	ds := &DataSupermarket{}
	now := time.Now()
	for i := 0; i < COUNT_FLAGS; i++ {
		ds.FlagDV[i] = ua.DataValue{
			Value:           ua.MustVariant(false),
			SourceTimestamp: now,
			ServerTimestamp: now,
			Status:          ua.StatusGood,
		}
	}
	for i := 0; i < COUNT_INT; i++ {
		ds.IntDV[i] = ua.DataValue{
			Value:           ua.MustVariant(int32(0)),
			SourceTimestamp: now,
			ServerTimestamp: now,
			Status:          ua.StatusGood,
		}
	}
	for i := 0; i < COUNT_REAL; i++ {
		ds.RealDV[i] = ua.DataValue{
			Value:           ua.MustVariant(float64(0)),
			SourceTimestamp: now,
			ServerTimestamp: now,
			Status:          ua.StatusGood,
		}
	}
	return ds
}

func (ds *DataSupermarket) UpdateFlag(idx int, v bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.Flags[idx] = v
	ds.FlagDV[idx].Value = ua.MustVariant(v)
	ds.FlagDV[idx].SourceTimestamp = time.Now()
	ds.FlagDV[idx].ServerTimestamp = time.Now()
}

func (ds *DataSupermarket) UpdateInt(idx int, v int32) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.IntValues[idx] = v
	ds.IntDV[idx].Value = ua.MustVariant(v)
	ds.IntDV[idx].SourceTimestamp = time.Now()
	ds.IntDV[idx].ServerTimestamp = time.Now()
}

func (ds *DataSupermarket) UpdateReal(idx int, v float64) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.RealValues[idx] = v
	ds.RealDV[idx].Value = ua.MustVariant(v)
	ds.RealDV[idx].SourceTimestamp = time.Now()
	ds.RealDV[idx].ServerTimestamp = time.Now()
}

func (ds *DataSupermarket) GetFlagDV(idx int) *ua.DataValue {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return &ds.FlagDV[idx]
}

func (ds *DataSupermarket) GetIntDV(idx int) *ua.DataValue {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return &ds.IntDV[idx]
}

func (ds *DataSupermarket) GetRealDV(idx int) *ua.DataValue {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return &ds.RealDV[idx]
}

type ArrayNS struct {
	srv  *server.Server
	name string
	id   uint16
	ds   *DataSupermarket
	mu   sync.RWMutex
}

func NewArrayNS(srv *server.Server, name string, ds *DataSupermarket) *ArrayNS {
	ns := &ArrayNS{srv: srv, name: name, ds: ds}
	srv.AddNamespace(ns)
	return ns
}

func (ns *ArrayNS) Name() string                        { return ns.name }
func (ns *ArrayNS) ID() uint16                          { return ns.id }
func (ns *ArrayNS) SetID(id uint16)                     { ns.id = id }
func (ns *ArrayNS) AddNode(n *server.Node) *server.Node { return n }
func (ns *ArrayNS) Node(id *ua.NodeID) *server.Node     { return nil }
func (ns *ArrayNS) Root() *server.Node                  { return nil }

func (ns *ArrayNS) Objects() *server.Node {
	oid := ua.NewNumericNodeID(ns.id, id.ObjectsFolder)
	typedef := ua.NewNumericExpandedNodeID(0, id.ObjectsFolder)
	return server.NewNode(
		oid,
		map[ua.AttributeID]*ua.DataValue{
			ua.AttributeIDNodeClass:     dv(int32(ua.NodeClassObject)),
			ua.AttributeIDBrowseName:    dv(&ua.QualifiedName{NamespaceIndex: 0, Name: ns.name}),
			ua.AttributeIDDisplayName:   dv(&ua.LocalizedText{EncodingMask: ua.LocalizedTextText, Text: ns.name}),
			ua.AttributeIDDescription:   dv(int32(0)),
			ua.AttributeIDDataType:      dv(typedef),
			ua.AttributeIDEventNotifier: dv(int16(0)),
		},
		[]*ua.ReferenceDescription{},
		nil,
	)
}

func dv(v any) *ua.DataValue {
	dt := time.Now()
	return &ua.DataValue{
		Value:           ua.MustVariant(v),
		SourceTimestamp: dt,
		ServerTimestamp: dt,
		Status:          ua.StatusGood,
	}
}

func (ns *ArrayNS) Browse(bd *ua.BrowseDescription) *ua.BrowseResult {
	ns.mu.RLock()
	defer ns.mu.RUnlock()

	if bd.NodeID.IntID() != id.RootFolder && bd.NodeID.IntID() != id.ObjectsFolder {
		return &ua.BrowseResult{StatusCode: ua.StatusGood, References: []*ua.ReferenceDescription{}}
	}

	if bd.NodeID.IntID() == id.RootFolder {
		refs := make([]*ua.ReferenceDescription, 1)
		newid := ua.NewNumericNodeID(ns.id, id.ObjectsFolder)
		expnewid := ua.NewNumericExpandedNodeID(ns.id, id.ObjectsFolder)
		refs[0] = &ua.ReferenceDescription{
			ReferenceTypeID: newid,
			NodeID:          expnewid,
			BrowseName:      &ua.QualifiedName{NamespaceIndex: ns.id, Name: "Objects"},
			DisplayName:     &ua.LocalizedText{EncodingMask: ua.LocalizedTextText, Text: "Objects"},
			TypeDefinition:  expnewid,
		}
		return &ua.BrowseResult{StatusCode: ua.StatusGood, References: refs}
	}

	totalNodes := COUNT_FLAGS + COUNT_INT + COUNT_REAL
	refs := make([]*ua.ReferenceDescription, 0, totalNodes)

	for i := 0; i < COUNT_FLAGS; i++ {
		nodeID := ua.NewNumericNodeID(ns.id, uint32(10000+i))
		refs = append(refs, &ua.ReferenceDescription{
			ReferenceTypeID: ua.NewNumericNodeID(0, id.HasComponent),
			IsForward:       true,
			NodeID:          ua.NewExpandedNodeID(nodeID, "", 0),
			BrowseName:      &ua.QualifiedName{NamespaceIndex: ns.ID(), Name: nodeName("Flag", i)},
			DisplayName:     &ua.LocalizedText{EncodingMask: ua.LocalizedTextText, Text: nodeName("Flag", i)},
			NodeClass:       ua.NodeClassVariable,
			TypeDefinition:  ua.NewExpandedNodeID(nodeID, "", 0),
		})
	}

	for i := 0; i < COUNT_INT; i++ {
		nodeID := ua.NewNumericNodeID(ns.id, uint32(20000+i))
		refs = append(refs, &ua.ReferenceDescription{
			ReferenceTypeID: ua.NewNumericNodeID(0, id.HasComponent),
			IsForward:       true,
			NodeID:          ua.NewExpandedNodeID(nodeID, "", 0),
			BrowseName:      &ua.QualifiedName{NamespaceIndex: ns.ID(), Name: nodeName("Int", i)},
			DisplayName:     &ua.LocalizedText{EncodingMask: ua.LocalizedTextText, Text: nodeName("Int", i)},
			NodeClass:       ua.NodeClassVariable,
			TypeDefinition:  ua.NewExpandedNodeID(nodeID, "", 0),
		})
	}

	for i := 0; i < COUNT_REAL; i++ {
		nodeID := ua.NewNumericNodeID(ns.id, uint32(30000+i))
		refs = append(refs, &ua.ReferenceDescription{
			ReferenceTypeID: ua.NewNumericNodeID(0, id.HasComponent),
			IsForward:       true,
			NodeID:          ua.NewExpandedNodeID(nodeID, "", 0),
			BrowseName:      &ua.QualifiedName{NamespaceIndex: ns.ID(), Name: nodeName("Real", i)},
			DisplayName:     &ua.LocalizedText{EncodingMask: ua.LocalizedTextText, Text: nodeName("Real", i)},
			NodeClass:       ua.NodeClassVariable,
			TypeDefinition:  ua.NewExpandedNodeID(nodeID, "", 0),
		})
	}

	return &ua.BrowseResult{StatusCode: ua.StatusGood, References: refs}
}

func nodeName(prefix string, i int) string {
	return fmt.Sprintf("%s%04d", prefix, i)
}

func (ns *ArrayNS) Attribute(n *ua.NodeID, a ua.AttributeID) *ua.DataValue {
	if n.Namespace() == ns.id {
		nid := n.IntID()
		switch {
		case nid >= 10000 && nid < 10000+COUNT_FLAGS:
			return ns.ds.GetFlagDV(int(nid - 10000))
		case nid >= 20000 && nid < 20000+COUNT_INT:
			return ns.ds.GetIntDV(int(nid - 20000))
		case nid >= 30000 && nid < 30000+COUNT_REAL:
			return ns.ds.GetRealDV(int(nid - 30000))
		}
	}

	if n.IntID() != id.ObjectsFolder {
		return &ua.DataValue{
			EncodingMask:    ua.DataValueStatusCode | ua.DataValueServerTimestamp,
			ServerTimestamp: time.Now(),
			Status:          ua.StatusBadNodeIDInvalid,
		}
	}
	return &ua.DataValue{
		EncodingMask:    ua.DataValueStatusCode | ua.DataValueServerTimestamp,
		ServerTimestamp: time.Now(),
		Status:          ua.StatusBadAttributeIDInvalid,
	}
}

func (ns *ArrayNS) SetAttribute(n *ua.NodeID, a ua.AttributeID, val *ua.DataValue) ua.StatusCode {
	if a != ua.AttributeIDValue {
		return ua.StatusBadNotWritable
	}

	if n.Namespace() != ns.id {
		return ua.StatusBadNodeIDUnknown
	}

	nid := n.IntID()
	v := val.Value.Value()

	switch {
	case nid >= 10000 && nid < 10000+COUNT_FLAGS:
		if b, ok := v.(bool); ok {
			ns.ds.UpdateFlag(int(nid-10000), b)
			ns.srv.ChangeNotification(ua.NewNumericNodeID(ns.id, uint32(nid)))
			return ua.StatusGood
		}
		return ua.StatusBadTypeMismatch

	case nid >= 20000 && nid < 20000+COUNT_INT:
		var iv int32
		switch val := v.(type) {
		case int32:
			iv = val
		case int16:
			iv = int32(val)
		case int64:
			iv = int32(val)
		case int:
			iv = int32(val)
		case uint32:
			iv = int32(val)
		case uint16:
			iv = int32(val)
		default:
			return ua.StatusBadTypeMismatch
		}
		ns.ds.UpdateInt(int(nid-20000), iv)
		ns.srv.ChangeNotification(ua.NewNumericNodeID(ns.id, uint32(nid)))
		return ua.StatusGood

	case nid >= 30000 && nid < 30000+COUNT_REAL:
		var fv float64
		switch val := v.(type) {
		case float64:
			fv = val
		case float32:
			fv = float64(val)
		default:
			return ua.StatusBadTypeMismatch
		}
		ns.ds.UpdateReal(int(nid-30000), fv)
		ns.srv.ChangeNotification(ua.NewNumericNodeID(ns.id, uint32(nid)))
		return ua.StatusGood
	}

	return ua.StatusBadNodeIDUnknown
}

func GenerateCert(endpoints []string, bits int, dur time.Duration) ([]byte, []byte, error) {
	return nil, nil, nil
}

func main() {
	flag.Parse()
	log.SetFlags(0)

	ds := NewDataSupermarket()
	log.Printf("DataSupermarket: %d flags, %d ints, %d reals (total %d)", COUNT_FLAGS, COUNT_INT, COUNT_REAL, COUNT_FLAGS+COUNT_INT+COUNT_REAL)

	var opts []server.Option
	opts = append(opts, server.EnableSecurity("None", ua.MessageSecurityModeNone))
	opts = append(opts, server.EnableAuthMode(ua.UserTokenTypeAnonymous))

	hostname, err := os.Hostname()
	if err != nil {
		log.Fatalf("Error getting hostname: %v", err)
	}

	opts = append(opts, server.EndPoint(*endpoint, *port))
	opts = append(opts, server.EndPoint("localhost", *port))
	opts = append(opts, server.EndPoint(hostname, *port))

	logger := Logger(1)
	opts = append(opts, server.SetLogger(logger))

	if *gencert {
		endpoints := []string{"localhost", hostname, *endpoint}
		c, k, err := GenerateCert(endpoints, 4096, time.Minute*60*24*365*10)
		if err != nil {
			log.Fatalf("problem creating cert: %v", err)
		}
		os.WriteFile(*certfile, c, 0)
		os.WriteFile(*keyfile, k, 0)
	}

	if *certfile != "" && *keyfile != "" {
		c, err := tls.LoadX509KeyPair(*certfile, *keyfile)
		if err == nil {
			if pk, ok := c.PrivateKey.(*rsa.PrivateKey); ok {
				opts = append(opts, server.PrivateKey(pk), server.Certificate(c.Certificate[0]))
			}
		}
	}

	opts = append(opts, server.ServerName("OPC UA Supermarket"))
	opts = append(opts, server.ProductName("High-Performance Data Supermarket"))

	s := server.New(opts...)

	ns := NewArrayNS(s, "DataSupermarket", ds)
	log.Printf("Namespace '%s' at index %d", ns.Name(), ns.ID())

	root_ns, _ := s.Namespace(0)
	root_obj := root_ns.Objects()
	root_obj.AddRef(ns.Objects(), id.HasComponent, true)

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		count := 0
		for range ticker.C {
			count++
			ds.UpdateInt(0, int32(count))
			ds.UpdateReal(0, float64(count)*1.5)
			ds.UpdateFlag(0, count%2 == 0)
		}
	}()

	log.Printf("Starting OPC UA Server on %s:%d", *endpoint, *port)

	if err := s.Start(context.Background()); err != nil {
		log.Fatalf("Error starting server: %s", err)
	}
	defer s.Close()

	sigch := make(chan os.Signal, 1)
	signal.Notify(sigch, os.Interrupt)
	signal.Stop(sigch)
	log.Printf("Press CTRL-C to exit")

	<-sigch
	log.Printf("Shutting down...")
}
