package installer

import (
	"sync"
)

// Prefetch downloads every asset the given specs need, concurrently,
// leaving them in the cache without installing anything.
//
// This is the half of a batch that is worth running in parallel. Measured
// on six real packages into an empty root: 4.3s with everything
// concurrent against 7.3s fully sequential -- but with the cache already
// warm, 0.9s against 1.2s. The whole win is the network; extraction,
// hooks and shims cost about 0.3s of it.
//
// So a batch fetches in parallel and then installs one at a time, which
// buys back two things for that 0.3s:
//
//   - **Order.** An install hook can call any goop-installed binary --
//     the shims directory is on its PATH. When a manifest's post_install
//     needs a binary another package in the same batch provides, and does
//     not declare it as a dependency (Scoop manifests routinely do not
//     declare runtime tools), concurrent installs made that a coin flip.
//     Reproduced at 3 successes in 6 before this. Installing in the order
//     the caller gave makes it predictable and puts it under their
//     control.
//   - **Readable output.** Concurrent installs interleave their progress
//     lines, so a failure in the middle of a batch is buried in unrelated
//     traffic from three other packages.
//
// Errors are deliberately not returned. A package that cannot be
// prefetched -- a dead URL, an unresolvable name -- must fail in the
// install itself, where the error is reported against that package with
// everything else that is known about it. Failing here would report the
// same problem twice, or worse, differently.
func Prefetch(specs []string) {
	concurrency := defaultConcurrency()
	if concurrency > len(specs) {
		concurrency = len(specs)
	}
	if concurrency < 1 {
		return
	}
	if len(specs) > 1 {
		// One line for the phase. The per-asset lines are suppressed:
		// several at once interleave into an unreadable block, and the
		// progress bars already show which transfers are live.
		Logf("fetching %d package(s)", len(specs))
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for _, spec := range specs {
		wg.Add(1)
		sem <- struct{}{}
		go func(spec string) {
			defer wg.Done()
			defer func() { <-sem }()
			// Best effort, and quiet about it: the install will say what
			// is wrong, in its own place, with its own context.
			_, _ = download(spec, true)
		}(spec)
	}
	wg.Wait()
}
