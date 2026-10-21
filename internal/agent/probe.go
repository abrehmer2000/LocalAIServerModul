package agent

// HostResources reports observations, not proof that an accelerator can execute
// this model. Callers should probe the runtime and require an actual answer too.
func HostResources(existingDirectory string) (gpu string, vram, free uint64) {
	gpu, vram = detectGPU()
	free, _ = freeSpace(existingDirectory)
	return
}
