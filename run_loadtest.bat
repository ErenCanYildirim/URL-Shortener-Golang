@echo off
echo Running URL Shortener Load Test...
echo.

if not exist loadtest mkdir loadtest 

if not exist loadtest\main.go (
    echo Error: loadtest\main.go not found!
    echo Is the load test code saved as loadtest\main.go? 
    pause 
    exit /b 1
)

go run loadtest\main.go 

echo.
echo Load test finished, press any key to exit...
pause > nul