#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <errno.h>
#include <string.h>
#include <seccomp.h>
#include <sys/stat.h>
#include <sys/types.h>

int main(void) {
    printf("=== Seccomp test: blocking mkdir/mkdirat ===\n\n");

    printf("[1] Before seccomp: trying mkdir /tmp/seccomp_before\n");
    if (mkdir("/tmp/seccomp_before", 0755) == 0) {
        printf("    mkdir SUCCEEDED (as expected without seccomp)\n");
        rmdir("/tmp/seccomp_before");
    } else {
        printf("    mkdir FAILED: %s (unexpected)\n", strerror(errno));
    }

    scmp_filter_ctx ctx = seccomp_init(SCMP_ACT_ALLOW);
    if (!ctx) { perror("seccomp_init"); return 1; }

    if (seccomp_rule_add(ctx, SCMP_ACT_ERRNO(EPERM), SCMP_SYS(mkdir), 0) < 0) {
        perror("seccomp_rule_add mkdir"); return 1;
    }
    if (seccomp_rule_add(ctx, SCMP_ACT_ERRNO(EPERM), SCMP_SYS(mkdirat), 0) < 0) {
        perror("seccomp_rule_add mkdirat"); return 1;
    }

    if (seccomp_load(ctx) < 0) { perror("seccomp_load"); return 1; }
    printf("\n[2] Seccomp loaded: mkdir/mkdirat blocked with EPERM\n");

    printf("\n[3] Other syscalls still work: writing to /tmp/seccomp_allowed\n");
    FILE *f = fopen("/tmp/seccomp_allowed", "w");
    if (f) {
        fprintf(f, "hello from sandboxed process\n");
        fclose(f);
        printf("    write() to file SUCCEEDED (not blocked)\n");
    } else {
        printf("    fopen() FAILED: %s\n", strerror(errno));
    }

    printf("\n[4] After seccomp: trying mkdir /tmp/seccomp_after\n");
    if (mkdir("/tmp/seccomp_after", 0755) == 0) {
        printf("    mkdir SUCCEEDED — FILTER NOT WORKING!\n");
    } else {
        printf("    mkdir FAILED: %s (as expected — blocked by seccomp)\n",
               strerror(errno));
    }

    seccomp_release(ctx);
    printf("\n=== Done ===\n");
    return 0;
}
