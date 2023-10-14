package constant

const (
	TypeFileProvider = "file"
	TypeHTTPProvider = "http"
)

func ProviderDisplayName(providerType string) string {
	switch providerType {
	case TypeFileProvider:
		return "File"
	case TypeHTTPProvider:
		return "HTTP"
	default:
		return "Unknown"
	}
}
