package sdk

// RouterOption configures the Router.
type RouterOption func(*Router)

// WithRouterEnabled enables or disables routing.
func WithRouterEnabled(enabled bool) RouterOption {
	return func(r *Router) {
		r.enabled = enabled
	}
}

// WithDecomposeThreshold sets the complexity score at which tasks are decomposed.
func WithDecomposeThreshold(threshold int) RouterOption {
	return func(r *Router) {
		r.decomposeThreshold = threshold
	}
}

// WithParallelThreshold sets the complexity score at which parallel execution is used.
func WithParallelThreshold(threshold int) RouterOption {
	return func(r *Router) {
		r.parallelThreshold = threshold
	}
}

// WithFastModel sets the model name to use for simple tasks.
func WithFastModel(model string) RouterOption {
	return func(r *Router) {
		r.fastModel = model
	}
}

// WithRouterOptimizer sets the strategy optimizer for learning-based routing.
func WithRouterOptimizer(optimizer *StrategyOptimizer) RouterOption {
	return func(r *Router) {
		r.optimizer = optimizer
	}
}
