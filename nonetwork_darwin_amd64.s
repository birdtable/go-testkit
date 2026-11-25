// The trampolines to libSystem's sandbox calls that nonetwork_darwin.go imports by name, as
// golang.org/x/sys/unix reaches libc: each jumps to the function and its address is the Go variable
// syscall.syscall6 is called with.

#include "textflag.h"

TEXT libc_sandbox_init_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_sandbox_init(SB)
GLOBL	·libcSandboxInitTrampolineAddr(SB), RODATA, $8
DATA	·libcSandboxInitTrampolineAddr(SB)/8, $libc_sandbox_init_trampoline<>(SB)

TEXT libc_sandbox_free_error_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_sandbox_free_error(SB)
GLOBL	·libcSandboxFreeErrorTrampolineAddr(SB), RODATA, $8
DATA	·libcSandboxFreeErrorTrampolineAddr(SB)/8, $libc_sandbox_free_error_trampoline<>(SB)

TEXT libc_sandbox_check_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_sandbox_check(SB)
GLOBL	·libcSandboxCheckTrampolineAddr(SB), RODATA, $8
DATA	·libcSandboxCheckTrampolineAddr(SB)/8, $libc_sandbox_check_trampoline<>(SB)
