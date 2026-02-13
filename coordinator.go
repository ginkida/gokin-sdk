package sdk

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TaskPriority defines the priority of a coordinated task.
type TaskPriority int

const (
	TaskPriorityLow    TaskPriority = 0
	TaskPriorityNormal TaskPriority = 1
	TaskPriorityHigh   TaskPriority = 2
)

// TaskStatus represents the status of a coordinated task.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusBlocked   TaskStatus = "blocked"
	TaskStatusReady     TaskStatus = "ready"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// CoordinatedTask is a task managed by the Coordinator.
type CoordinatedTask struct {
	ID           string
	Prompt       string
	AgentType    AgentType
	Priority     TaskPriority
	Dependencies []string // task IDs that must complete first
	Status       TaskStatus
	Result       *AgentResult
}

// CoordinatorStatus summarizes the state of all coordinated tasks.
type CoordinatorStatus struct {
	Total     int
	Pending   int
	Blocked   int
	Ready     int
	Running   int
	Completed int
	Failed    int
}

// Coordinator manages parallel and sequential task execution with dependencies.
type Coordinator struct {
	runner      *Runner
	tasks       map[string]*CoordinatedTask
	mu          sync.RWMutex
	maxParallel int

	onTaskStart    func(taskID string, task *CoordinatedTask)
	onTaskComplete func(taskID string, task *CoordinatedTask)
}

// NewCoordinator creates a new task coordinator.
func NewCoordinator(runner *Runner, maxParallel int) *Coordinator {
	if maxParallel <= 0 {
		maxParallel = 3
	}
	return &Coordinator{
		runner:      runner,
		tasks:       make(map[string]*CoordinatedTask),
		maxParallel: maxParallel,
	}
}

// SetOnTaskStart sets a callback invoked when a task starts.
func (c *Coordinator) SetOnTaskStart(fn func(taskID string, task *CoordinatedTask)) {
	c.onTaskStart = fn
}

// SetOnTaskComplete sets a callback invoked when a task completes.
func (c *Coordinator) SetOnTaskComplete(fn func(taskID string, task *CoordinatedTask)) {
	c.onTaskComplete = fn
}

// AddTask adds a new task to the coordinator.
func (c *Coordinator) AddTask(id, prompt string, agentType AgentType, priority TaskPriority, deps []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	status := TaskStatusReady
	if len(deps) > 0 {
		status = TaskStatusBlocked
	}

	c.tasks[id] = &CoordinatedTask{
		ID:           id,
		Prompt:       prompt,
		AgentType:    agentType,
		Priority:     priority,
		Dependencies: deps,
		Status:       status,
	}
}

// RunAll executes all tasks respecting dependencies and parallelism limits.
func (c *Coordinator) RunAll(ctx context.Context) (map[string]*AgentResult, error) {
	results := make(map[string]*AgentResult)
	var resultsMu sync.Mutex

	for {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		ready := c.getReadyTasks()
		if len(ready) == 0 {
			if c.allDone() {
				break
			}
			// Wait for running tasks to complete
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Limit parallel execution
		running := c.countRunning()
		available := c.maxParallel - running
		if available <= 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if len(ready) > available {
			ready = ready[:available]
		}

		// Launch ready tasks
		var wg sync.WaitGroup
		for _, task := range ready {
			c.mu.Lock()
			task.Status = TaskStatusRunning
			c.mu.Unlock()

			if c.onTaskStart != nil {
				c.onTaskStart(task.ID, task)
			}

			wg.Add(1)
			go func(t *CoordinatedTask) {
				defer wg.Done()

				agentTask := AgentTask{
					Prompt:      t.Prompt,
					Type:        t.AgentType,
					Description: t.ID,
				}

				_, result, err := c.runner.Spawn(ctx, agentTask)

				c.mu.Lock()
				if err != nil || (result != nil && result.Error != nil) {
					t.Status = TaskStatusFailed
				} else {
					t.Status = TaskStatusCompleted
				}
				t.Result = result
				c.unblockDependentsLocked(t.ID)
				c.mu.Unlock()

				resultsMu.Lock()
				results[t.ID] = result
				resultsMu.Unlock()

				if c.onTaskComplete != nil {
					c.onTaskComplete(t.ID, t)
				}
			}(task)
		}

		wg.Wait()
	}

	return results, nil
}

// RunParallel runs a list of independent tasks in parallel (no dependencies).
func (c *Coordinator) RunParallel(ctx context.Context, tasks []AgentTask) ([]*AgentResult, error) {
	results := make([]*AgentResult, len(tasks))
	var wg sync.WaitGroup
	var mu sync.Mutex
	semaphore := make(chan struct{}, c.maxParallel)

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, t AgentTask) {
			defer wg.Done()

			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				mu.Lock()
				results[idx] = &AgentResult{Error: ctx.Err()}
				mu.Unlock()
				return
			}

			_, result, err := c.runner.Spawn(ctx, t)
			if err != nil && result == nil {
				result = &AgentResult{Error: err}
			}
			mu.Lock()
			results[idx] = result
			mu.Unlock()
		}(i, task)
	}

	wg.Wait()
	return results, nil
}

// RunSequential runs tasks one after another.
func (c *Coordinator) RunSequential(ctx context.Context, tasks []AgentTask) ([]*AgentResult, error) {
	results := make([]*AgentResult, 0, len(tasks))

	for _, task := range tasks {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		_, result, err := c.runner.Spawn(ctx, task)
		results = append(results, result)
		if err != nil {
			return results, err
		}
	}

	return results, nil
}

// GetStatus returns a summary of all task statuses.
func (c *Coordinator) GetStatus() CoordinatorStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var s CoordinatorStatus
	s.Total = len(c.tasks)

	for _, t := range c.tasks {
		switch t.Status {
		case TaskStatusPending:
			s.Pending++
		case TaskStatusBlocked:
			s.Blocked++
		case TaskStatusReady:
			s.Ready++
		case TaskStatusRunning:
			s.Running++
		case TaskStatusCompleted:
			s.Completed++
		case TaskStatusFailed:
			s.Failed++
		}
	}

	return s
}

// GetTask returns a task by ID.
func (c *Coordinator) GetTask(id string) (*CoordinatedTask, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	task, ok := c.tasks[id]
	return task, ok
}

// CancelTask cancels a task and its dependents.
func (c *Coordinator) CancelTask(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	task, ok := c.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	task.Status = TaskStatusFailed
	task.Result = &AgentResult{Error: fmt.Errorf("cancelled")}

	// Cancel dependents recursively
	for _, t := range c.tasks {
		for _, dep := range t.Dependencies {
			if dep == id {
				t.Status = TaskStatusFailed
				t.Result = &AgentResult{Error: fmt.Errorf("dependency %s was cancelled", id)}
			}
		}
	}

	return nil
}

func (c *Coordinator) getReadyTasks() []*CoordinatedTask {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var ready []*CoordinatedTask
	for _, t := range c.tasks {
		if t.Status == TaskStatusReady {
			ready = append(ready, t)
		}
	}

	// Sort by priority (higher first)
	for i := 1; i < len(ready); i++ {
		for j := i; j > 0 && ready[j].Priority > ready[j-1].Priority; j-- {
			ready[j], ready[j-1] = ready[j-1], ready[j]
		}
	}

	return ready
}

func (c *Coordinator) countRunning() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	count := 0
	for _, t := range c.tasks {
		if t.Status == TaskStatusRunning {
			count++
		}
	}
	return count
}

func (c *Coordinator) allDone() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, t := range c.tasks {
		if t.Status != TaskStatusCompleted && t.Status != TaskStatusFailed {
			return false
		}
	}
	return true
}

func (c *Coordinator) unblockDependents(completedID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unblockDependentsLocked(completedID)
}

// unblockDependentsLocked transitions blocked tasks to ready when dependencies are met.
// Caller must hold c.mu.
func (c *Coordinator) unblockDependentsLocked(completedID string) {
	for _, t := range c.tasks {
		if t.Status != TaskStatusBlocked {
			continue
		}

		allMet := true
		anyFailed := false
		for _, dep := range t.Dependencies {
			depTask, ok := c.tasks[dep]
			if !ok {
				allMet = false
				continue
			}
			if depTask.Status == TaskStatusFailed {
				anyFailed = true
				break
			}
			if depTask.Status != TaskStatusCompleted {
				allMet = false
			}
		}

		if anyFailed {
			t.Status = TaskStatusFailed
			t.Result = &AgentResult{Error: fmt.Errorf("dependency failed")}
		} else if allMet {
			t.Status = TaskStatusReady
		}
	}
}
