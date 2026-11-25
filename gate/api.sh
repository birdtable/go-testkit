# The API gate, in shell, for contain alone: the root and taskfile run cmd/apigate, and contain
# moves to it in a change of its own. contain-api's
# cmds source it with the module set in a literal word of Taskfile.yml, in contain's folder, so
# go-task's own interpreter runs it on every system:
#
#   mod=contain; . "{{.ROOT_DIR}}/gate/api.sh"
#
# contain keeps its files in contain/api/, and each check runs from this one host for all three
# systems, with GOOS set:
#
#   - The cross-system gate: apidiff (tools/apidiff.mod) writes the module's export data for darwin,
#     linux and windows, and compares darwin's with each of the others, the full report, since an
#     added name is "compatible". Each line, as "darwin to <system>: <apidiff's line>", sorted, must
#     equal api/systems.txt's lines. So a name or a signature one system alone has is red, and a line
#     the file keeps that no longer happens is red too. A module that does not build for a system is
#     red by itself, never an empty report.
#   - The marks loop: api/marks.txt names each name that is not experimental, as "<package> <name>
#     <mark>". A name it does not list is experimental, so today it lists none. Each line's name must
#     still be there, by go doc.
#   - The tag diff: the base is the last contain/v* tag reachable from the head. apidiff reads the base's tree, written out
#     of the repository by git read-tree and git checkout-index through an index of its own, with no
#     network and the repository's index untouched, for each system, and compares it with the head's,
#     its incompatible lines alone. Those lines, sorted and with each repeat once, must equal
#     api/breaks/<base>.txt, the base's version alone in the name, for example api/breaks/v0.1.0.txt. No file for the base means no break is declared. With no tag
#     yet, the tag diff is skipped with a line that says so, and the two checks above have run.
#
# In each file a line that starts with # and a blank line are not read. The module is set by the
# Taskfile's word, and every other name is set here before it is read. For contain, the member
# has run CONTAIN_WORK first: GOWORK names its workspace file, and its folder, w, is removed when
# the command ends. This file's own folder goes inside it, so that one removal takes both.
#
# On Windows go-task's interpreter cannot append an external program's output to a file (>> answers
# "Bad file descriptor"), and its own tar takes no -C.
# So each program writes a file of its own with >, the files are joined after by sort, and no pipe
# or tar is used: go-files' gate/api.sh at 3d26115, which go-proc's follows.
unset IFS
set -o pipefail
case "$mod" in
contain)
	tags='contain/v*'
	;;
*)
	echo "api: the module is '$mod', and wants contain: the root and taskfile run cmd/apigate" >&2
	exit 1
	;;
esac
fail() {
	echo "api ($mod): $1" >&2
	exit 1
}
top=$(git rev-parse --show-toplevel) || fail "git cannot read this repository, so no tag can be found"
bin=$(cd "$top" && GOWORK=off CGO_ENABLED=0 go tool -modfile=tools/apidiff.mod -n apidiff) || fail "apidiff could not be built from tools/apidiff.mod"
d="$w/api"
mkdir -p "$d" || fail "the folder $d could not be made"
# By its real path: go matches the folder it runs in with a workspace's use lines, and macOS's
# temporary folder is reached through a link (/var is /private/var).
d=$(cd "$d" && pwd -P) || fail "the folder $d could not be read"

# lines writes the lines of the file $1 that are read, a line that starts with # and a blank one left
# out, to $2. A missing file is red.
lines() {
	[ -f "$1" ] || fail "$1 is missing"
	awk '!/^#/ && NF' "$1" >"$2" || fail "$1 could not be read"
}

# report writes apidiff's lines for old $2 and new $3, each without its "- ", to $4; $1 is -incompatible
# or all. A compare apidiff could not make is red.
report() {
	if [ "$1" = all ]; then
		"$bin" -m "$2" "$3" >"$d/out" 2>"$d/err" || { cat "$d/err" >&2; fail "apidiff could not compare $2 with $3"; }
	else
		"$bin" -m -incompatible "$2" "$3" >"$d/out" 2>"$d/err" || { cat "$d/err" >&2; fail "apidiff could not compare $2 with $3"; }
	fi
	awk '/^- / { print substr($0, 3) }' "$d/out" >"$4" || fail "apidiff's report could not be read"
}

# The head's export data, one file a system.
for os in darwin linux windows; do
	GOOS=$os CGO_ENABLED=0 "$bin" -m -w "$d/$os.exp" . 2>"$d/err" || { cat "$d/err" >&2; fail "the module does not build for $os, so its surface there cannot be read"; }
done

# The cross-system gate.
for os in linux windows; do
	report all "$d/darwin.exp" "$d/$os.exp" "$d/report-$os"
	awk -v os="$os" '{ print "darwin to " os ": " $0 }' "$d/report-$os" >"$d/systems-$os" || fail "apidiff's report could not be read"
done
LC_ALL=C sort "$d/systems-linux" "$d/systems-windows" >"$d/systems.got" || fail "the cross-system report could not be sorted"
lines api/systems.txt "$d/systems.want"
git diff --no-index --exit-code "$d/systems.want" "$d/systems.got" ||
	fail "the systems' surfaces differ other than api/systems.txt says (the - lines are the file's, the + lines the module's)"

# The marks loop.
lines api/marks.txt "$d/marks"
while read -r pkg name mark rest; do
	if [ -z "$mark" ] || [ -n "$rest" ]; then
		fail "api/marks.txt has the line '$pkg $name $mark $rest', and wants a package, a name and its mark"
	fi
	if [ "$mark" = experimental ]; then
		fail "api/marks.txt marks $pkg $name experimental, which every name it does not list is"
	fi
	go doc "$pkg" "$name" >/dev/null 2>&1 || fail "api/marks.txt marks $pkg $name, which go doc does not find"
done <"$d/marks"

# The tag diff. Only git's own answers of no tag ("No names found", "No tags can describe") mean none: any
# other failure of git describe is red, and so is a shallow clone, whose missing history would read as no tag.
[ "$(git -C "$top" rev-parse --is-shallow-repository)" = false ] ||
	fail "this clone is shallow, so its last $tags tag cannot be found; fetch its whole history (git fetch --unshallow)"
if base=$(git -C "$top" describe --tags --abbrev=0 --match "$tags" 2>"$d/err"); then
	:
elif grep -q -e "No names found" -e "No tags can describe" "$d/err"; then
	base=""
else
	cat "$d/err" >&2
	fail "git describe could not read the $tags tags"
fi
if [ -z "$base" ]; then
	echo "api ($mod): no $tags tag is reachable from the head, so the tag diff is skipped; the cross-system gate and the marks loop ran"
	exit 0
fi
mkdir -p "$d/old" || fail "the folder $d/old could not be made"
GIT_INDEX_FILE="$d/index" git -C "$top" read-tree "$base" 2>"$d/err" || { cat "$d/err" >&2; fail "the tree of $base could not be read"; }
GIT_INDEX_FILE="$d/index" git -C "$top" checkout-index -a --prefix="$d/old/" 2>"$d/err" || { cat "$d/err" >&2; fail "the tree of $base could not be written out"; }
old="$d/old"
if [ "$mod" = contain ]; then
	# contain at the base requires the root at the version its go.mod names; the root of the same tree
	# stands for that version, as CONTAIN_WORK's root stands for the head's. go work writes the file
	# GOWORK names, so the head's workspace above this folder is not found.
	old="$d/old/contain"
	oldwork="$d/old.work"
	ver=$(awk '$1 == "github.com/birdtable/go-testkit" && $2 ~ /^v/ { print $2; exit } $1 == "require" && $2 == "github.com/birdtable/go-testkit" { print $3; exit }' "$old/go.mod")
	[ -n "$ver" ] || fail "contain's go.mod at $base names no version of the root"
	{ GOWORK="$oldwork" go work init "$d/old" "$old" && GOWORK="$oldwork" go work edit -replace="github.com/birdtable/go-testkit@$ver=$d/old"; } 2>"$d/err" ||
		{ cat "$d/err" >&2; fail "the workspace file for $base could not be written"; }
fi
for os in darwin linux windows; do
	(cd "$old" && GOWORK="$oldwork" GOOS=$os CGO_ENABLED=0 "$bin" -m -w "$d/old-$os.exp" .) 2>"$d/err" ||
		{ cat "$d/err" >&2; fail "$base does not build for $os, so its surface there cannot be read"; }
	report -incompatible "$d/old-$os.exp" "$d/$os.exp" "$d/breaks-$os"
done
LC_ALL=C sort -u "$d/breaks-darwin" "$d/breaks-linux" "$d/breaks-windows" >"$d/breaks.got" || fail "the tag report could not be sorted"
declared="api/breaks/${base#*/}.txt"
if [ -f "$declared" ]; then
	lines "$declared" "$d/breaks.want"
else
	: >"$d/breaks.want"
fi
git diff --no-index --exit-code "$d/breaks.want" "$d/breaks.got" ||
	fail "the breaks since $base are not what $declared declares (the - lines are the file's, the + lines the module's; no file declares none)"
echo "api ($mod): the systems agree as api/systems.txt says, the marks name what is there, and the breaks since $base are what $declared declares"
