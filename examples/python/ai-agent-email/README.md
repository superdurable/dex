# Table of Contents

1. [What it is](#what-it-is)
2. [How to Run](#how-to-run)  
   2.1. [Prerequisites](#prerequisites)  
   2.2. [Environment Variables](#environment-variables)  
   2.3. [Backend Setup](#backend-setup)  
   2.4. [Frontend Setup](#frontend-setup)  
   2.5. [Run the Application](#run-the-application)
3. [Using the Application](#using-the-application)

## What it is

The AI Agent Email is a feature-rich application that leverages AI to streamline email composition
and management. It combines a React frontend with a Flask backend powered by iWF (Indeed Workflow Framework) to
provide a durable, scalable email assistant.

### Why iWF?

iWF (Indeed Workflow Framework) was chosen for this application due to its ability to provide extreme **simplicity** in
software design and implementation. By leveraging iWF, developers can focus on building business logic without worrying
about the complexities of distributed systems, state management, or scalability.

For more details on building reliable AI agents with iWF on Temporal, check out the following
article: [Build Reliable AI Agents with iWF on Temporal](https://medium.com/@qlong/build-reliable-ai-agents-with-iwf-on-temporal-7f1a101e000b)

### Functional Features

- **AI-Powered Email Composition**: Generate professionally written emails based on simple user requests and inputs
- **Email Translation**: Translate emails into different languages upon request
- **Smart Scheduling**: Schedule emails to be sent at specific times, supporting both absolute and relative time
  expressions (e.g., "tomorrow", "in 2 hours")
- **Auto-Save Draft**: Automatically saves draft inputs every 5 seconds to prevent work loss
- **Cancel Operation**: Ability to cancel scheduled emails before they're sent

### Technical Features

- **Workflow Checkpointing**: Uses iWF to maintain workflow state, enabling resumption from previous points after any
  instance restart
- **Scalable Architecture**: Capable of handling **billions** of concurrent workflows
- **Durable Timers**: Schedule-based operations use server-side durable timers that persist across system restarts
- **Server-Side Backoff Retry**: Built-in retry mechanism orchestrated on the server side as distributed system, not
  dependent on a single machine.
- **Stateful API Integration**: Seamless integration with OpenAI's GPT models with context retention
- **Durable Drafting System**: Preserves user input across page refreshes and browser sessions

### Demo Videos

#### AI Agent Email Demo(happy case)

[![AI Agent Email Demo](https://img.youtube.com/vi/2dvIetECHWg/0.jpg)](https://www.youtube.com/watch?v=2dvIetECHWg "AI Agent Email Demo")

#### iWF Framework Overview(cancel a scheduled email)

[![iWF Framework Overview](https://img.youtube.com/vi/EEHSLYkbREU/0.jpg)](https://www.youtube.com/watch?v=EEHSLYkbREU "iWF Framework Overview")

## How to Run

### Prerequisites

Make sure you have the following installed and set up:

1. Run iWF server
    * The simplest way is to run
      `docker pull iworkflowio/iwf-server-lite:latest && docker run -p 8801:8801 -p 7233:7233 -p 8233:8233 -e AUTO_FIX_WORKER_URL=host.docker.internal --add-host host.docker.internal:host-gateway -it iworkflowio/iwf-server-lite:latest`
    * Or check out [iWF documentation](https://github.com/superdurable/iwf?tab=readme-ov-file#how-to-use)
2. [Python 3.13.2](https://www.python.org/downloads/)
2. [Poetry](https://python-poetry.org/docs/) for dependency management and virtual environments.
3. [Node.js and npm](https://nodejs.org/) for building the frontend.
4. All required environment variables.

### Environment Variables

Before running the script, ensure the following environment variables are properly configured in your system:

- `OPENAI_API_KEY`: Your OpenAI API key.
- `GOOGLE_EMAIL_ADDRESS`: The email address you'll use for sending emails. E.g. qlong.seattle@gmail.com
- `GOOGLE_EMAIL_APP_PASSWORD`: The application password for your Google account. You can go
  to [Google app password](security.google.com/settings/security/apppasswords) to create a password for your account.

You can set these variables in your shell profile file (e.g., `.bashrc`, `.zshrc`, etc.) or export them before running
the script:

```bash
export OPENAI_API_KEY="your-openai-api-key"
export GOOGLE_EMAIL_ADDRESS="your-email-address"
export GOOGLE_EMAIL_APP_PASSWORD="your-app-password"
```

### Backend Setup

Once Poetry is installed, navigate to the project directory and install the required dependencies by running:

```bash
poetry install
```

This will create a virtual environment and install all necessary packages listed in the `pyproject.toml` file.

### Frontend Setup

To set up the React TypeScript frontend:

1. Install the required npm packages:

```bash
cd ai-agent-email
npm install
```

2. Build the frontend:

```bash
npm run build
```

For development, you can use the watch mode:

```bash
npm start
```

### Run the Application

To start the application, execute the following command:

```bash
poetry run python ai-agent-email/main.py
```

This will launch the Flask application at http://localhost:8802. Open your browser and navigate to this URL to access
the AI Agent Email interface.

## Using the Application

1. When you first open the application, you'll see a text box and a "Talk to Agent" button.
2. Enter your request in the text box (e.g., "Help me write an email to thank John for his help on the project").
3. Click the "Talk to Agent" button to send your request to the AI agent.
4. The agent will process your request and provide a response that will be displayed on the page.
