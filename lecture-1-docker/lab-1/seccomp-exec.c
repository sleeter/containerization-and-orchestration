// seccomp-exec: ставит seccomp-фильтр на себя и делает exec нужной программы.
// Фильтр наследуется через exec, поэтому действует уже на запущенную программу.
//
// Сборка:  gcc -o seccomp-exec seccomp-exec.c -lseccomp
// Запуск:  ./seccomp-exec <команда> [аргументы...]
#include <errno.h>
#include <seccomp.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "usage: %s <command> [args...]\n", argv[0]);
        return 2;
    }

    // По умолчанию разрешаем всё, блокируем точечно.
    scmp_filter_ctx ctx = seccomp_init(SCMP_ACT_ALLOW);
    if (!ctx) {
        perror("seccomp_init");
        return 1;
    }

    // Запрещённые вызовы отвечают EPERM, а не убивают процесс.
    int rc = seccomp_rule_add(ctx, SCMP_ACT_ERRNO(EPERM), SCMP_SYS(mkdir), 0);
    if (rc < 0) {
        fprintf(stderr, "seccomp_rule_add(mkdir): %s\n", strerror(-rc));
        return 1;
    }
    rc = seccomp_rule_add(ctx, SCMP_ACT_ERRNO(EPERM), SCMP_SYS(mkdirat), 0);
    if (rc < 0) {
        fprintf(stderr, "seccomp_rule_add(mkdirat): %s\n", strerror(-rc));
        return 1;
    }

    rc = seccomp_load(ctx);
    if (rc < 0) {
        fprintf(stderr, "seccomp_load: %s\n", strerror(-rc));
        return 1;
    }
    seccomp_release(ctx);

    // С этого момента фильтр уже действует. Следующий execve проходит,
    // потому что мы не запрещали execve.
    execvp(argv[1], &argv[1]);
    perror("execvp");
    return 127;
}
