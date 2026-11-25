# The budget step of each tier, on go-proc's and go-files' gate/budget.sh. A tier's cmds source it
# with its tier and step set in literal words of Taskfile.yml, so go-task's own interpreter runs it on
# every system and no sh need be on the path:
#
#   tier=pre-commit; step=start; . ./gate/budget.sh
#
# pre-commit and release start and end with it. start reads the 1-minute load with hatch, then
# writes the tier's start in seconds and that load to .task/budget/<tier>.<pid>. The pid is go-task's
# own ($$ in its interpreter): every command of one run has it, and no run beside it does, so two
# runs of one tier in one tree each time themselves. end reads that file, removes it, and has
# `hatch budget` (tools/hatch.mod) hold the elapsed seconds to the tier's budget in budgets.yaml.
# hatch holds no policy, so this file holds testkit's: which row applies on this kind of host, and
# that a pre-commit whose load at its start was above 15 passes past its budget, saying so, the
# tier's every other step being green for its budget step to run at all. A release past its budget
# fails at any load.
#
# pre-push is one step, cpu: `hatch budget --cpu` runs the tier's members, `task <tier>-members`, in a
# tree of its own, and holds their CPU time, user plus system, to the tier's <tier>-cpu_limit row.
# CPU time is the same on a quiet machine and a loaded one, so no load excuses anything: an overrun
# fails at any load, and a tier under its CPU row passes over its wall row. The wall row,
# <tier>-budget, is the hang guard only: hatch kills a tree still running at ten times it, and fails
# it as hung. hatch prints the CPU time, the wall time and the load. On a kind of host with no row the
# members run alone and the time is printed. No tier skips, and none is tried again.
#
# Nothing here is taken from a Task var or the caller's environment: a start or load kept in a var
# would be whatever the caller's environment said, as `LOAD=16 git push`. The tier and step are set
# by the Taskfile's words, and every other name is set here before it is read. GITHUB_ACTIONS is
# read, as the one name from the environment that decides anything: it marks a hosted runner, whose
# job's timeout holds it, so the release budget is not applied there.
unset IFS
case "$tier" in
pre-commit | pre-push | release) ;;
*)
	echo "budget: the tier is '$tier', and wants pre-commit, pre-push or release" >&2
	exit 1
	;;
esac
f=".task/budget/$tier.$$"
case "$step" in
start)
	# hatch reads the load the same way on each system; where there is none (Windows) it fails, and
	# no load is written. The load is read before the start, so hatch's first build is not timed.
	load=$(go tool -modfile=tools/hatch.mod hatch budget --load --window 1 2>/dev/null) || load=""
	mkdir -p .task/budget || exit 1
	printf '%s %s\n' "$(date +%s)" "$load" >"$f" || exit 1
	exit 0
	;;
end) ;;
cpu)
	# The row is the tier's own on the kind of host budgets.yaml names; elsewhere there is none, and
	# the time is printed. hatch is built before the start, so its first build is not timed. Each row
	# is a key, a colon, a space and whole seconds: <tier>-budget the wall row and <tier>-cpu_limit the
	# CPU row, whose id is the tier. A tier with no CPU row is refused by hatch, by name.
	go tool -modfile=tools/hatch.mod hatch budget --load --window 1 >/dev/null 2>&1 || true
	start=$(date +%s)
	host=$(uname -sm 2>/dev/null | tr ' ' '-')
	if [ "$host" != "$(awk -F': *' '$1 == "host" { print $2 }' budgets.yaml)" ]; then
		go tool -modfile=tools/task.mod task "$tier-members" || exit 1
		echo "$tier took $(($(date +%s) - start))s; no budget for this kind of host ($host), so none applies"
		exit 0
	fi
	set -- --cpu --cpu-row '^(?P<id>[a-z-]+)-cpu_limit: (?P<limit>[0-9]+)$' --budgets budgets.yaml --row '^(?P<id>[a-z-]+)-budget: (?P<limit>[0-9]+)$'
	set -- "$@" --tier "$tier" --since "$start" --window 1 --noun "$tier"
	go tool -modfile=tools/hatch.mod hatch budget "$@" --advice "a tier over its CPU budget is a speed bug to find; budgets.yaml has the budgets" -- go tool -modfile=tools/task.mod task "$tier-members"
	exit
	;;
*)
	echo "budget: the step is '$step', and wants start, end or cpu" >&2
	exit 1
	;;
esac
if [ ! -f "$f" ]; then
	echo "budget: $f is missing, so $tier has no start to time from" >&2
	exit 1
fi
start=""
load=""
rest=""
read -r start load rest <"$f"
rm -f "$f"
# Each field is checked before it is used: the start as whole seconds with no leading 0, which $(( ))
# and hatch's --since would read as octal, and the load as digits with one dot inside, or nothing,
# so no spelling a number parser reads (1e9, 0x20) passes for a load.
case "$start" in
'' | 0* | *[!0-9]*)
	echo "budget: $f gives the start as '$start', and wants whole seconds" >&2
	exit 1
	;;
esac
case "$load" in
*[!0-9.]* | .* | *. | *.*.*)
	echo "budget: $f gives the load as '$load', and wants a load such as 3.21, or none" >&2
	exit 1
	;;
esac
if [ -n "$rest" ]; then
	echo "budget: $f has '$rest' after the start and the load, and wants nothing more" >&2
	exit 1
fi
elapsed=$(($(date +%s) - start))
if [ "$elapsed" -lt 0 ]; then
	echo "budget: $tier started at $start, after now, so its time would be ${elapsed}s" >&2
	exit 1
fi
# The row: the tier's own on the kind of host budgets.yaml names; the release budget on Linux or
# Windows amd64 off a hosted runner; and none elsewhere, where the time is printed.
host=$(uname -sm 2>/dev/null | tr ' ' '-')
row=""
if [ "$host" = "$(awk -F': *' '$1 == "host" { print $2 }' budgets.yaml)" ]; then
	row=$tier-budget
elif [ "$tier" = release ] && [ -z "${GITHUB_ACTIONS:-}" ]; then
	case "$host" in
	Linux-x86_64 | MINGW64_NT-*-x86_64 | MSYS_NT-*-x86_64) row=release-budget-session ;;
	esac
fi
if [ -z "$row" ]; then
	at=""
	if [ -n "$load" ]; then at=" at load $load"; fi
	echo "$tier took ${elapsed}s$at; no budget for this kind of host ($host), so none applies"
	exit 0
fi
set -- --budgets budgets.yaml --row '^(?P<id>[a-z-]+): (?P<limit>[0-9]+)$' --tier "$row" --since "$start" --window 1 --noun "$tier"
if [ -n "$load" ]; then set -- "$@" --load-before "$load"; fi
if [ "$tier" = pre-commit ]; then set -- "$@" --limit 15 --loaded-when before --excused "passed at the load it started at"; fi
go tool -modfile=tools/hatch.mod hatch budget "$@" --advice "a tier over its budget is a speed bug to find; budgets.yaml has the budgets"
