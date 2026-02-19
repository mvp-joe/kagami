#!/usr/bin/env bash
set -euo pipefail

SESSION_ID="kagami-20260218-172531"
SESSION="exercise-$SESSION_ID"
PROJECT_DIR="/home/joe/code/kagami"
EXERCISER_DIR="/tmp/claude-exercise-$SESSION_ID"
BUILDER_INIT="$PROJECT_DIR/.exercise/sessions/$SESSION_ID/builder-init.md"

# Kill existing session if present
tmux kill-session -t "$SESSION" 2>/dev/null || true

# Create tmux session — builder pane (left)
tmux new-session -d -s "$SESSION" -n main -x 220 -y 55

# Builder: start interactive claude, wait for init, send first prompt
tmux send-keys -t "$SESSION:main" "cd \"$PROJECT_DIR\" && claude --dangerously-skip-permissions" Enter
sleep 8
tmux send-keys -t "$SESSION:main" "Read $BUILDER_INIT and follow those instructions exactly. Begin now." Enter

# Exerciser pane (right) — claude picks up CLAUDE.md from cwd automatically
tmux split-window -h -t "$SESSION:main"
tmux send-keys -t "$SESSION:main.1" "cd \"$EXERCISER_DIR\" && claude --dangerously-skip-permissions" Enter
sleep 8
tmux send-keys -t "$SESSION:main.1" "Read your CLAUDE.md and follow those instructions exactly. Begin now." Enter

# Monitor pane (bottom) — Redis message stream
tmux split-window -v -t "$SESSION:main.0" -l 12
tmux send-keys -t "$SESSION:main.2" \
  "redis-cli MONITOR 2>/dev/null | grep --line-buffered '$SESSION_ID'" Enter

# Select builder pane
tmux select-pane -t "$SESSION:main.0"

echo ""
echo "Adversarial testing launched!"
echo "  tmux attach -t $SESSION"
echo ""
echo "Layout: builder (left) | exerciser (right) | redis monitor (bottom)"
echo "Session ID: $SESSION_ID"
