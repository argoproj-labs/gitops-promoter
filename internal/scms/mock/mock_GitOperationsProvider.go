// Code generated by mockery v2.42.2. DO NOT EDIT.

package mock

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	v1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// MockGitOperationsProvider is an autogenerated mock type for the GitOperationsProvider type
type MockGitOperationsProvider struct {
	mock.Mock
}

type MockGitOperationsProvider_Expecter struct {
	mock *mock.Mock
}

func (_m *MockGitOperationsProvider) EXPECT() *MockGitOperationsProvider_Expecter {
	return &MockGitOperationsProvider_Expecter{mock: &_m.Mock}
}

// GetGitHttpsRepoUrl provides a mock function with given fields: gitRepo
func (_m *MockGitOperationsProvider) GetGitHttpsRepoUrl(gitRepo v1alpha1.GitRepository) string {
	ret := _m.Called(gitRepo)

	if len(ret) == 0 {
		panic("no return value specified for GetGitHttpsRepoUrl")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func(v1alpha1.GitRepository) string); ok {
		r0 = rf(gitRepo)
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// MockGitOperationsProvider_GetGitHttpsRepoUrl_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetGitHttpsRepoUrl'
type MockGitOperationsProvider_GetGitHttpsRepoUrl_Call struct {
	*mock.Call
}

// GetGitHttpsRepoUrl is a helper method to define mock.On call
//   - gitRepo v1alpha1.GitRepository
func (_e *MockGitOperationsProvider_Expecter) GetGitHttpsRepoUrl(gitRepo interface{}) *MockGitOperationsProvider_GetGitHttpsRepoUrl_Call {
	return &MockGitOperationsProvider_GetGitHttpsRepoUrl_Call{Call: _e.mock.On("GetGitHttpsRepoUrl", gitRepo)}
}

func (_c *MockGitOperationsProvider_GetGitHttpsRepoUrl_Call) Run(run func(gitRepo v1alpha1.GitRepository)) *MockGitOperationsProvider_GetGitHttpsRepoUrl_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(v1alpha1.GitRepository))
	})
	return _c
}

func (_c *MockGitOperationsProvider_GetGitHttpsRepoUrl_Call) Return(_a0 string) *MockGitOperationsProvider_GetGitHttpsRepoUrl_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *MockGitOperationsProvider_GetGitHttpsRepoUrl_Call) RunAndReturn(run func(v1alpha1.GitRepository) string) *MockGitOperationsProvider_GetGitHttpsRepoUrl_Call {
	_c.Call.Return(run)
	return _c
}

// GetToken provides a mock function with given fields: ctx
func (_m *MockGitOperationsProvider) GetToken(ctx context.Context) (string, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for GetToken")
	}

	var r0 string
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (string, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) string); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Get(0).(string)
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockGitOperationsProvider_GetToken_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetToken'
type MockGitOperationsProvider_GetToken_Call struct {
	*mock.Call
}

// GetToken is a helper method to define mock.On call
//   - ctx context.Context
func (_e *MockGitOperationsProvider_Expecter) GetToken(ctx interface{}) *MockGitOperationsProvider_GetToken_Call {
	return &MockGitOperationsProvider_GetToken_Call{Call: _e.mock.On("GetToken", ctx)}
}

func (_c *MockGitOperationsProvider_GetToken_Call) Run(run func(ctx context.Context)) *MockGitOperationsProvider_GetToken_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *MockGitOperationsProvider_GetToken_Call) Return(_a0 string, _a1 error) *MockGitOperationsProvider_GetToken_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockGitOperationsProvider_GetToken_Call) RunAndReturn(run func(context.Context) (string, error)) *MockGitOperationsProvider_GetToken_Call {
	_c.Call.Return(run)
	return _c
}

// GetUser provides a mock function with given fields: ctx
func (_m *MockGitOperationsProvider) GetUser(ctx context.Context) (string, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for GetUser")
	}

	var r0 string
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (string, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) string); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Get(0).(string)
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockGitOperationsProvider_GetUser_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetUser'
type MockGitOperationsProvider_GetUser_Call struct {
	*mock.Call
}

// GetUser is a helper method to define mock.On call
//   - ctx context.Context
func (_e *MockGitOperationsProvider_Expecter) GetUser(ctx interface{}) *MockGitOperationsProvider_GetUser_Call {
	return &MockGitOperationsProvider_GetUser_Call{Call: _e.mock.On("GetUser", ctx)}
}

func (_c *MockGitOperationsProvider_GetUser_Call) Run(run func(ctx context.Context)) *MockGitOperationsProvider_GetUser_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *MockGitOperationsProvider_GetUser_Call) Return(_a0 string, _a1 error) *MockGitOperationsProvider_GetUser_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockGitOperationsProvider_GetUser_Call) RunAndReturn(run func(context.Context) (string, error)) *MockGitOperationsProvider_GetUser_Call {
	_c.Call.Return(run)
	return _c
}

// NewMockGitOperationsProvider creates a new instance of MockGitOperationsProvider. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockGitOperationsProvider(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockGitOperationsProvider {
	mock := &MockGitOperationsProvider{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
