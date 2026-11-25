// Package secondconsumer is testkit as a second business would use it: its tests run testkit's and
// fake's main paths through the exported API alone, with values deliberately unlike testkit's own, and
// fail wherever testkit fills in a value of ours in place of the caller's. It has no code of its own;
// it runs in the commit tier, so a pull request that puts a value of ours back where the caller's
// belongs goes red there. Its folder sweep is the consumer's own stand-in, a main package with a step
// of its own.
package secondconsumer
