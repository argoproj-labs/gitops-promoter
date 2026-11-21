# Controller load test

`hack/loadtest` measures how the controller's CPU and memory grow with the number of
PromotionStrategies (PS), ChangeTransferPolicies (CTP) and gates. It also captures pprof
profiles at each step, so you can see where the memory goes.

## What it runs

Everything runs locally. No cluster or SCM is needed.

| Piece | How |
|---|---|
| kube-apiserver + etcd | envtest binaries (`bin/k8s/...`) |
| Git server | in-process [gitkit](https://github.com/sosedoff/gitkit) on `127.0.0.1:5001` |
| SCM | the controller's built-in `fake` provider (PRs held in memory in the controller) |
| Controller | the real `cmd` binary as a **separate process** with `--pprof-bind-address`, so its RSS and CPU are not mixed with the harness, apiserver or git server |

Each PromotionStrategy gets:

- its own git repository (`loadtest/ps-NNNNN`), seeded with `git fast-import` so thousands are cheap to create
- a Secret, ScmProvider (fake), GitRepository
- `-envs` environments (default 3), so 3 CTPs, and PRs while promoting
- a DependentsSuccessfulCommitStatus (always created, because `orderCommitStatusRef` is required)
- optional gates (`-gates`): `timed` (TimedCommitStatus as an active gate) and `git` (GitCommitStatus as a proposed gate)

Every repo starts fully promoted (each active branch equals its proposed branch), so after the
initial reconcile the controller is idle.

## Per level

Levels are cumulative. For example, `-levels 100,250,500,1000` adds 150 PS to reach 250.

1. Create the new PS and wait until every PS reports every environment on the latest dry SHA (**initial reconcile time**, ramp CPU).
2. Wait `-settle`, then measure for `-idle-window`: RSS, Go heap, goroutines, controller CPU, and the CPU of git subprocesses. Take CPU, heap (`?gc=1`) and goroutine profiles.
3. For each `-waves`: push a new dry commit and hydrated commits to every repo at once, send push webhooks, and measure the time for every PS to promote through all environments. Record CPU, peak RSS and peak heap over that time, plus a CPU profile and a mid-wave heap profile.

## Running

```sh
make envtest && bin/setup-envtest-release-0.25 use 1.37.0 --bin-dir bin -p path
go build -o /tmp/promoter ./cmd
go run ./hack/loadtest -controller-bin /tmp/promoter \
  -levels 100,250,500,1000 -settle 60s -idle-window 150s -waves 1 \
  -out /tmp/lt
hack/loadtest/analyze.sh /tmp/lt /tmp/promoter   # writes /tmp/lt/analysis.md
```

Useful flags:

- `-gates timed,git|git|none`: which extra gates to create
- `-envs N`: environments per PS
- `-qps N`, `-max-concurrent-reconciles N`: override every work queue in the shipped ControllerConfiguration (0 keeps the shipped values)
- `-keep`: leave everything running at the end so you can run `go tool pprof http://127.0.0.1:6060/debug/pprof/heap` yourself

## Output

```
<out>/results.md          summary tables
<out>/results.json        all numbers, including per-controller reconciles/sec and reconcile-seconds/sec
<out>/L0100/heap-idle.pb.gz, cpu-idle.pb.gz, goroutine-idle.txt
<out>/L0100/cpu-wave1.pb.gz, heap-wave1.pb.gz
<out>/controller.log
<out>/controllerconfiguration.yaml   the config the controller ran with
```

Compare two levels to see what grows:

```sh
go tool pprof -top -sample_index=inuse_space -diff_base <out>/L0100/heap-idle.pb.gz <out>/L1000/heap-idle.pb.gz
```

## Caveats

- The apiserver, etcd, git server and controller share one machine, so CPU-bound phases (initial reconcile, waves) compete for cores. Idle and memory numbers are the most portable.
- The fake SCM keeps PR state in process-global maps, so its memory shows up in the profile. A real SCM provider doesn't hold that, but it has HTTP client and caching costs of its own.
- `git` subprocess memory is not in the controller's RSS. Their CPU is reported separately as "git CPU" (reaped children only).
- The default 5m `requeueDuration` means idle CPU depends on the window length. Windows shorter than 5m undercount periodic resyncs.
