// Package catalog records the operator's preferred model; it never claims installation or engine validation.
package catalog

const DefaultAlias = "gemma-4-26b-a4b"

type Profile struct {
	Name         string `json:"name"`
	Alias        string `json:"alias"`
	Repository   string `json:"repository"`
	Quantization string `json:"quantization"`
	Source       string `json:"source"`
	License      string `json:"license"`
}

func Default() Profile {
	return Profile{Name: "Gemma 4 26B A4B", Alias: DefaultAlias, Repository: "google/gemma-4-26B-A4B-it-qat-q4_0-gguf", Quantization: "QAT Q4_0 · GGUF", Source: "https://huggingface.co/google/gemma-4-26B-A4B-it-qat-q4_0-gguf", License: "Apache-2.0"}
}
