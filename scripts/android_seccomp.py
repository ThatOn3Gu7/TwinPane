#!/usr/bin/env python3
"""android_seccomp.py - run a program under an Android-like seccomp filter.

Android runs every app (Termux included) under a seccomp filter. System calls
outside its allowlist are answered with SIGSYS, which kills a Go program
("SIGSYS: bad system call" plus a register dump). The calls that matter for Go
programs built with GOOS=linux:

  439 faccessat2         blocked on every Android version (os/exec.LookPath)
  424 pidfd_send_signal  \\
  434 pidfd_open          > blocked on Android 8-10 (os.StartProcess, Go >= 1.23)
  438 pidfd_getfd        /

This launcher traps exactly those (SECCOMP_RET_TRAP -> SIGSYS, like Android)
when they come from a non-PIE Go binary, and makes every other syscall numbered
424 or higher (and the trapped ones from glibc programs) fail with ENOSYS, so
glibc programs started underneath (bash, nvim) keep working through their
normal fallbacks, as Termux's bionic programs do. Syscall numbers >= 424 are
the same on x86_64 and arm64.

usage: android_seccomp.py [--allow-pidfd] -- PROGRAM [ARGS...]
  --allow-pidfd   emulate Android 11+ (only faccessat2 is trapped)
"""
import ctypes, os, struct, sys

TRAP_ALWAYS = [439]
TRAP_PIDFD = [424, 434, 438]

PR_SET_NO_NEW_PRIVS, PR_SET_SECCOMP, SECCOMP_MODE_FILTER = 38, 22, 2
RET_ALLOW, RET_TRAP, RET_ERRNO = 0x7FFF0000, 0x00030000, 0x00050000
ENOSYS = 38
ARCHES = {"x86_64": 0xC000003E, "aarch64": 0xC00000B7}
LD_W_ABS, JEQ_K, JGE_K, RET_K = 0x20, 0x15, 0x35, 0x06


def ins(code, jt, jf, k):
    return struct.pack("HBBI", code, jt, jf, k)


def assemble(items):
    """items: tuples (code, jt_label|int, jf_label|int, k) or label strings."""
    pos, out, n = {}, [], 0
    for it in items:
        if isinstance(it, str):
            pos[it] = n
        else:
            n += 1
    i = 0
    for it in items:
        if isinstance(it, str):
            continue
        code, jt, jf, k = it
        rel = lambda j: (pos[j] - i - 1) if isinstance(j, str) else j
        out.append(ins(code, rel(jt), rel(jf), k))
        i += 1
    return b"".join(out)


def build(trap):
    """Trap the listed syscalls when they are made by code below 256 MiB (a
    non-PIE Go binary is loaded at 0x400000); the same calls from shared
    libraries / PIE programs (glibc bash, nvim) fail with ENOSYS instead, so
    those fall back as on a system without the call. Android's bionic tools
    never make these calls, so this keeps child programs realistic."""
    arch = ARCHES[os.uname().machine]
    p = [(LD_W_ABS, 0, 0, 4), (JEQ_K, 0, "allow", arch), (LD_W_ABS, 0, 0, 0)]
    for nr in trap:
        p.append((JEQ_K, "checkip", 0, nr))
    p += [(JGE_K, 0, "allow", 424), (RET_K, 0, 0, RET_ERRNO | ENOSYS),
          "checkip", (LD_W_ABS, 0, 0, 12), (JEQ_K, 0, "enosys", 0),
          (LD_W_ABS, 0, 0, 8), (JGE_K, "enosys", 0, 0x10000000),
          (RET_K, 0, 0, RET_TRAP),
          "enosys", (RET_K, 0, 0, RET_ERRNO | ENOSYS),
          "allow", (RET_K, 0, 0, RET_ALLOW)]
    return assemble(p)


def main():
    args = sys.argv[1:]
    trap = TRAP_ALWAYS + TRAP_PIDFD
    if args and args[0] == "--allow-pidfd":
        trap, args = TRAP_ALWAYS, args[1:]
    if args and args[0] == "--":
        args = args[1:]
    if not args:
        sys.exit(__doc__)
    raw = build(trap)
    n = len(raw) // 8
    buf = ctypes.create_string_buffer(raw, len(raw))
    class Fprog(ctypes.Structure):
        _fields_ = [("len", ctypes.c_ushort), ("filter", ctypes.c_void_p)]
    fprog = Fprog(n, ctypes.cast(buf, ctypes.c_void_p))
    libc = ctypes.CDLL(None, use_errno=True)
    libc.prctl.argtypes = [ctypes.c_int, ctypes.c_ulong, ctypes.c_void_p, ctypes.c_ulong, ctypes.c_ulong]
    if libc.prctl(PR_SET_NO_NEW_PRIVS, 1, None, 0, 0) != 0:
        sys.exit("prctl(NO_NEW_PRIVS) failed: %s" % os.strerror(ctypes.get_errno()))
    if libc.prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, ctypes.byref(fprog), 0, 0) != 0:
        sys.exit("prctl(SECCOMP) failed: %s" % os.strerror(ctypes.get_errno()))
    os.execvp(args[0], args)


if __name__ == "__main__":
    main()
