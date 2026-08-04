package jobs

import "github.com/riverqueue/river"

// workerRegistry is every worker this process runs. Adding one here is all
// that is required to make it available.
//
// The list is registration functions rather than worker values because
// river.AddWorker is generic over the job args: a []river.Worker cannot be
// written down, but a slice of closures that each add one can.
var workerRegistry = []func(*river.Workers){}
