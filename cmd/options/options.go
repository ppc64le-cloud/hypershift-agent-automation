package options

type ClusterOptions struct {
	Name           string
	NameSpace      string
	BaseDomain     string
	PullSecretFile string
	SSHKeyFile     string
	ReleaseImage   string
	NodeCount      int
}
