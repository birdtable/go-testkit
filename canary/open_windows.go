package canary

import "os"

// openFlags opens a file Files reads. Windows has no FIFO in a folder, so an open never waits on one.
const openFlags = os.O_RDONLY
