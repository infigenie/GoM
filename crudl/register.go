package crudl

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"

	gomHTTP "github.com/hauxe/gom/http"
	sdklog "github.com/hauxe/gom/log"

	"github.com/pkg/errors"

	"github.com/jmoiron/sqlx"
)

// Register register crud methods
func Register(db *sqlx.DB, table string, object Object, options ...Option) (crud *CRUD, routes []gomHTTP.ServerRoute, err error) {
	if db == nil || table == "" || object == nil {
		return nil, nil, errors.New("invalid config")
	}
	obj := object.Get()

	rv := reflect.ValueOf(obj)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, nil, errors.New("object type is nil")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, nil, errors.Errorf("object type is not struct <%s>", rv.Kind().String())
	}
	crud = &CRUD{
		Config: &Config{
			DB:        db,
			TableName: table,
			Object:    object,
		},
	}
	crud.Logger, err = sdklog.NewFactory()

	if err != nil {
		return nil, nil, err
	}
	// set up options
	for _, op := range options {
		if err := op(crud.Config); err != nil {
			return nil, nil, err
		}
	}
	if err = crud.scanStructMySQL(rv); err != nil {
		return nil, nil, err
	}
	if len(crud.Config.fields) == 0 {
		return nil, nil, errors.Errorf("table %s doesnt specify any working field", crud.Config.TableName)
	}
	if crud.Config.pk == nil {
		return nil, nil, errors.Errorf("table %s doesnt specify the primary key", crud.Config.TableName)
	}
	if crud.Config.C {
		// create "create" route handler
		routes = append(routes, crud.registerC())
	}
	if crud.Config.R {
		// create "get" route handler
		routes = append(routes, crud.registerR())
	}
	if crud.Config.U {
		routes = append(routes, crud.registerU())
	}
	if crud.Config.D {
		// create "delete" route handler
		routes = append(routes, crud.registerD())
	}
	if crud.Config.L {
		// create "list" route handler
		routes = append(routes, crud.registerL())
	}
	return
}

// UseC use create handler
func UseC() Option {
	return func(config *Config) error {
		config.C = true
		return nil
	}
}

// UseR use read handler
func UseR() Option {
	return func(config *Config) error {
		config.R = true
		return nil
	}
}

// UseU use update handler
func UseU() Option {
	return func(config *Config) error {
		config.U = true
		return nil
	}
}

// UseD use update handler
func UseD() Option {
	return func(config *Config) error {
		config.D = true
		return nil
	}
}

// UseL use list handler
func UseL() Option {
	return func(config *Config) error {
		config.L = true
		return nil
	}
}

// SetValidators set validators
func SetValidators(validators map[string]Validator) Option {
	return func(config *Config) error {
		config.validatorMux.Lock()
		defer config.validatorMux.Unlock()
		if config.Validators == nil {
			config.Validators = make(map[string]Validator, len(validators))
		}
		for key, validator := range validators {
			config.Validators[key] = validator
		}
		return nil
	}
}

func (crud *CRUD) registerC() gomHTTP.ServerRoute {
	// build create sql
	fields := crud.Config.createFields
	if len(fields) == 0 {
		// allow create all fields
		fields = crud.Config.fields
	}
	fieldNames := make([]string, len(fields))
	for i, field := range fields {
		fieldNames[i] = field.name
	}
	crud.Config.createdFields = fields
	crud.Config.sqlCRUDCreate = fmt.Sprintf(sqlCRUDCreate, crud.Config.TableName,
		strings.Join(fieldNames, ","), ":"+strings.Join(fieldNames, ",:"))
	// build validator
	validators := []gomHTTP.ParamValidator{}
	for _, field := range fieldNames {
		if validatorName, ok := crud.Config.fieldValidators[field]; ok {
			if validator, ok := crud.Config.Validators[validatorName]; ok {
				validators = append(validators, getMethodValidator("create", validator))
			}
		}
	}
	return gomHTTP.ServerRoute{
		Name:       "crud_create_" + crud.Config.TableName,
		Method:     http.MethodPost,
		Path:       fmt.Sprintf("/%s", crud.Config.TableName),
		Validators: validators,
		Handler:    crud.handleCreate,
	}
}

func (crud *CRUD) registerR() gomHTTP.ServerRoute {
	// build select sql
	fields := crud.Config.selectFields
	if len(fields) == 0 {
		// allow select all fields
		fields = crud.Config.fields
	}
	fieldNames := make([]string, len(fields))
	for i, field := range fields {
		fieldNames[i] = field.name
	}
	crud.Config.selectedFields = fields
	crud.Config.sqlCRUDRead = fmt.Sprintf(sqlCRUDRead, strings.Join(fieldNames, ","),
		crud.Config.TableName, crud.Config.pk.name)
	return gomHTTP.ServerRoute{
		Name:       "crud_read_" + crud.Config.TableName,
		Method:     http.MethodGet,
		Path:       fmt.Sprintf("/%s", crud.Config.TableName),
		Validators: []gomHTTP.ParamValidator{validatePrimaryKey(crud.Config.pk.index)},
		Handler:    crud.handleRead,
	}
}

func (crud *CRUD) registerU() gomHTTP.ServerRoute {
	// build update sql
	fields := crud.Config.updateFields
	if len(fields) == 0 {
		// allow update all fields
		fields = crud.Config.fields
	}
	fieldNames := make([]string, len(fields))
	for i, field := range fields {
		fieldNames[i] = field.name
	}
	crud.Config.updatedFields = fields
	names := make([]string, len(fieldNames))
	for i, field := range fieldNames {
		names[i] = fmt.Sprintf("`%s` = :%s", field, field)
	}
	crud.Config.sqlCRUDUpdate = fmt.Sprintf(sqlCRUDUpdate, crud.Config.TableName,
		strings.Join(names, ","), crud.Config.pk.name, crud.Config.pk.name)
	// build validator
	validators := []gomHTTP.ParamValidator{validatePrimaryKey(crud.Config.pk.index)}
	for _, field := range fieldNames {
		if validatorName, ok := crud.Config.fieldValidators[field]; ok {
			if validator, ok := crud.Config.Validators[validatorName]; ok {
				validators = append(validators, getMethodValidator("update", validator))
			}
		}
	}
	return gomHTTP.ServerRoute{
		Name:       "crud_update_" + crud.Config.TableName,
		Method:     http.MethodPatch,
		Path:       fmt.Sprintf("/%s", crud.Config.TableName),
		Validators: validators,
		Handler:    crud.handleUpdate,
	}
}

func (crud *CRUD) registerD() gomHTTP.ServerRoute {
	// build create sql
	crud.Config.sqlCRUDDelete = fmt.Sprintf(sqlCRUDDelete, crud.Config.TableName,
		crud.Config.pk.name)
	return gomHTTP.ServerRoute{
		Name:       "crud_delete_" + crud.Config.TableName,
		Method:     http.MethodDelete,
		Path:       fmt.Sprintf("/%s", crud.Config.TableName),
		Validators: []gomHTTP.ParamValidator{validatePrimaryKey(crud.Config.pk.index)},
		Handler:    crud.handleDelete,
	}
}

func (crud *CRUD) registerL() gomHTTP.ServerRoute {
	// build list sql
	fields := crud.Config.listFields
	if len(fields) == 0 {
		// allow update all fields
		fields = crud.Config.fields
	}
	fieldNames := make([]string, len(fields))
	for i, field := range fields {
		fieldNames[i] = field.name
	}
	crud.Config.listedFields = fields
	crud.Config.sqlCRUDList = fmt.Sprintf(sqlCRUDList, "`"+strings.Join(fieldNames, "`,`")+"`",
		crud.Config.TableName, crud.Config.pk.name)
	fmt.Println(crud.Config.sqlCRUDList)
	return gomHTTP.ServerRoute{
		Name:    "crud_list_" + crud.Config.TableName,
		Method:  http.MethodGet,
		Path:    fmt.Sprintf("/%s/list", crud.Config.TableName),
		Handler: crud.handleList,
	}
}
