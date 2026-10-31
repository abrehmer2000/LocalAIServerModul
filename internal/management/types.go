// Package management defines the small, versioned agent management protocol.
package management

type Model struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Filename   string `json:"filename"`
	Commit     string `json:"commit"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
}
type Plan struct {
	Generation string `json:"generation"`
	Action     string `json:"action"` // download, start, stop
	Model      Model  `json:"model"`
	GPU        string `json:"gpu"` // auto or cpu
}
type Report struct {
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	CPUs       int    `json:"cpus"`
	GPU        string `json:"gpu,omitempty"`
	VRAMBytes  uint64 `json:"vram_bytes,omitempty"`
	FreeBytes  uint64 `json:"free_bytes,omitempty"`
	State      string `json:"state"`
	Generation string `json:"generation,omitempty"`
	Downloaded int64  `json:"downloaded,omitempty"`
	Total      int64  `json:"total,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	Error      string `json:"error,omitempty"` // fixed error codes; never raw engine/download output
}
