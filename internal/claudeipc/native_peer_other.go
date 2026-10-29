//go:build !darwin && !linux

package claudeipc

func nativePeerCredentials(int) (int, uint32, error) { return 0, 0, rebind() }

func processIdentity(int) (nativeProcess, error) { return nativeProcess{}, rebind() }

func processWithParent(int) (nativeProcess, int, error) { return nativeProcess{}, 0, rebind() }
