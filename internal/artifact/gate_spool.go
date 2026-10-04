package artifact

import "os"

// OpenGateSpool removes the directory entry before sensitive bytes are written.
// Closing the descriptor, including on process exit, releases the artifact.
func OpenGateSpool() (*os.File, error) {
	file, err := os.CreateTemp("", "durpdeploy-gate-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(file.Name()); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
