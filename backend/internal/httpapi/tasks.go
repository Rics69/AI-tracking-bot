package httpapi

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/tasks"
)

func toAPITask(o tasks.Occurrence) Task {
	return Task{
		Id: o.ID, Title: o.Title, Date: openapi_types.Date{Time: o.Date}, Time: optional(o.DueTime),
		Repeat: TaskRepeat(o.Repeat), GoalId: o.GoalID, Done: o.Done,
	}
}

func taskNow(t tasks.Task) Task {
	return toAPITask(tasks.Occurrence{Task: t, Date: t.DueOn, Done: t.DoneAt != nil})
}

func (s *Server) ListTasks(ctx context.Context, req ListTasksRequestObject) (ListTasksResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	list, err := s.tasks.Calendar(ctx, u.TelegramID, req.Params.From.Time, req.Params.To.Time)
	if err != nil {
		return nil, err
	}
	items := make([]Task, len(list))
	for i, o := range list {
		items[i] = toAPITask(o)
	}
	return ListTasks200JSONResponse{Items: items}, nil
}

func (s *Server) CreateTask(ctx context.Context, req CreateTaskRequestObject) (CreateTaskResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	t := tasks.Task{UserID: u.TelegramID, Title: b.Title, DueOn: b.Date.Time, DueTime: deref(b.Time), GoalID: b.GoalId}
	if b.Repeat != nil {
		t.Repeat = string(*b.Repeat)
	}
	saved, err := s.tasks.Create(ctx, t, s.today(u))
	if err != nil {
		return nil, err
	}
	s.events.Track(ctx, u.TelegramID, events.TaskCreated, events.Props{"via": events.ViaMiniApp})
	return CreateTask201JSONResponse(taskNow(saved)), nil
}

func (s *Server) UpdateTask(ctx context.Context, req UpdateTaskRequestObject) (UpdateTaskResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p := tasks.Patch{Title: b.Title, DueOn: dateTime(b.Date), DueTime: b.Time}
	if b.Repeat != nil {
		r := string(*b.Repeat)
		p.Repeat = &r
	}
	saved, err := s.tasks.Update(ctx, u.TelegramID, req.Id, p, s.today(u))
	if err != nil {
		return nil, err
	}
	return UpdateTask200JSONResponse(taskNow(saved)), nil
}

func (s *Server) DeleteTask(ctx context.Context, req DeleteTaskRequestObject) (DeleteTaskResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.tasks.Delete(ctx, u.TelegramID, req.Id); err != nil {
		return nil, err
	}
	return DeleteTask204Response{}, nil
}

func (s *Server) DoneTask(ctx context.Context, req DoneTaskRequestObject) (DoneTaskResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	saved, err := s.tasks.Done(ctx, u.TelegramID, req.Id, s.today(u), s.family.Now())
	if err != nil {
		return nil, err
	}
	s.events.Track(ctx, u.TelegramID, events.TaskDone, nil)
	return DoneTask200JSONResponse(taskNow(saved)), nil
}
