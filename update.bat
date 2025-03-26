@echo off

del /f /q go.sum >nul 2>&1
del /f /q go.mod >nul 2>&1

go mod init fisher

go mod tidy

go build fisher.go
echo The update was successful!