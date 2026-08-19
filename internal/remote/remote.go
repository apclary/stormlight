// Package remote reaches a Stormlight host that is not this one.
//
// The daemon runs on the machine the agents run on — that is the whole
// design. An agent's PTY, its provider process, its hooks, its transcript
// file, and the repository it is working in all live together; what
// crosses the network is the windrunner wire protocol and nothing else.
// A remote host is therefore not a different kind of runtime. It is the
// same runtime over a different transport: `ssh <host> stormlight
// _wrbridge`, which ensures a daemon on that host and then splices its
// own stdio onto that daemon's socket.
//
// The trust boundary comes along unchanged. The wire protocol has no
// authentication of its own and does not need any: reaching the socket
// means being the user who owns it, enforced by directory permissions
// locally and by the host's own login remotely. The bridge runs as that
// user. Nothing binds a port.
package remote

// Protocol is the remote contract this build speaks. It changes when the
// framing around the tunnel changes or when the dashboard begins invoking a
// new command on the far-side binary. The latter matters just as much: an
// older binary otherwise accepts the bridge and fails later with an opaque
// subcommand exit.
const Protocol = 2

type ProtocolRelation uint8

const (
	RemoteProtocolOlder ProtocolRelation = iota
	ProtocolCompatible
	RemoteProtocolNewer
)

// CompareProtocol classifies a remote contract against this build's.
func CompareProtocol(remote int) ProtocolRelation {
	switch {
	case remote < Protocol:
		return RemoteProtocolOlder
	case remote > Protocol:
		return RemoteProtocolNewer
	default:
		return ProtocolCompatible
	}
}

// Hello is the one line a bridge writes before it becomes a byte pipe.
// It answers the questions dispatch would otherwise have to guess at from
// the local machine, where every answer is wrong: where the remote
// binary lives (hooks re-invoke it through $STORMLIGHT_BIN), which socket
// directory its daemon is on (a hook's own event call has to reach the
// same one), and whether the two ends can talk at all.
type Hello struct {
	Protocol  int    `json:"protocol"`
	Version   string `json:"version"`
	Bin       string `json:"bin"`
	SocketDir string `json:"socket_dir"`
	Hostname  string `json:"hostname"`
}
