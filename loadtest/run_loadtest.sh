#!/bin/bash

echo "Running URL Shortener Load Test..."
echo 

if [! -f main.go]; then 
    echo "Error main.go not found under loadtest/main.go"
    echo "Is the code saved as main.go under loadtest?"
    read -p "Press Enter to exit..."
    exit 1
fi  

go run main.go

echo 
read -p "Load test finished, press Enter to exit"