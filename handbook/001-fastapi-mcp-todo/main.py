"""
ToDo API
========

A simple FastAPI application that manages a ToDo list stored in a local
SQLite database. Supports full CRUD operations on todo items.

Run with:
    uvicorn main:app --reload

Interactive docs:
    http://127.0.0.1:8000/docs   (Swagger UI)
    http://127.0.0.1:8000/redoc  (ReDoc)

MCP server (via fastapi-mcp):
    http://127.0.0.1:8000/mcp    (Streamable HTTP transport)
    The /todos endpoints are exposed as MCP tools, named after each
    route's operation_id (e.g. list_todos, create_todo).
"""

import os
import sqlite3
from collections.abc import Iterator
from contextlib import asynccontextmanager, contextmanager
from pathlib import Path

from fastapi import FastAPI, HTTPException, Path as PathParam, status
from fastapi_mcp import FastApiMCP
from pydantic import BaseModel, Field

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

# SQLite database file. Defaults to next to this script; set TODO_DB_PATH to
# store it elsewhere (e.g. on a persistent disk when deployed).
DB_PATH = Path(os.getenv("TODO_DB_PATH", Path(__file__).parent / "todos.db"))


# ---------------------------------------------------------------------------
# Database helpers
# ---------------------------------------------------------------------------

@contextmanager
def get_connection() -> Iterator[sqlite3.Connection]:
    """Open a SQLite connection, commit on success, and always close it."""
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row  # Access columns by name
    try:
        yield conn
        conn.commit()
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()


def init_db() -> None:
    """Create the todos table if it does not already exist."""
    DB_PATH.parent.mkdir(parents=True, exist_ok=True)  # Ensure folder exists
    with get_connection() as conn:
        conn.execute(
            """
            CREATE TABLE IF NOT EXISTS todos (
                todo_id   INTEGER PRIMARY KEY AUTOINCREMENT,
                content   TEXT    NOT NULL,
                completed INTEGER NOT NULL DEFAULT 0
            )
            """
        )


def row_to_todo(row: sqlite3.Row) -> "Todo":
    """Convert a database row into a Todo response model."""
    return Todo(
        todo_id=row["todo_id"],
        content=row["content"],
        completed=bool(row["completed"]),
    )


# ---------------------------------------------------------------------------
# Pydantic schemas (request / response models)
# ---------------------------------------------------------------------------

class TodoCreate(BaseModel):
    """Payload for creating a new todo."""

    content: str = Field(..., min_length=1, examples=["Buy groceries"])
    completed: bool = Field(default=False, examples=[False])


class TodoUpdate(BaseModel):
    """Payload for updating a todo. Only the provided fields are changed."""

    content: str | None = Field(default=None, min_length=1, examples=["Buy milk"])
    completed: bool | None = Field(default=None, examples=[True])


class Todo(BaseModel):
    """A todo item as returned by the API."""

    todo_id: int = Field(..., examples=[1])
    content: str = Field(..., examples=["Buy groceries"])
    completed: bool = Field(default=False, examples=[False])


class Message(BaseModel):
    """Simple message response."""

    message: str


# ---------------------------------------------------------------------------
# Application setup
# ---------------------------------------------------------------------------

@asynccontextmanager
async def lifespan(_: FastAPI):
    """Initialize the database when the app starts."""
    init_db()
    yield


app = FastAPI(
    title="ToDo API",
    description="A simple CRUD API for managing todo items, backed by SQLite.",
    version="1.0.0",
    lifespan=lifespan,
)

# Reusable path parameter definition for todo_id
TodoIdParam = PathParam(..., ge=1, description="The ID of the todo item")


def fetch_todo_or_404(conn: sqlite3.Connection, todo_id: int) -> sqlite3.Row:
    """Return the todo row with the given ID, or raise a 404 error."""
    row = conn.execute(
        "SELECT todo_id, content, completed FROM todos WHERE todo_id = ?",
        (todo_id,),
    ).fetchone()
    if row is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail=f"Todo with id {todo_id} not found",
        )
    return row


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------

@app.get("/", response_model=Message, tags=["Root"], summary="Welcome message")
def read_root() -> Message:
    """Return a basic welcome message."""
    return Message(message="Welcome to the ToDo API! Visit /docs for the API docs.")


@app.get(
    "/todos",
    response_model=list[Todo],
    tags=["Todos"],
    summary="Get all todos",
    operation_id="list_todos",
)
def get_todos() -> list[Todo]:
    """Return every todo item, ordered by ID."""
    with get_connection() as conn:
        rows = conn.execute(
            "SELECT todo_id, content, completed FROM todos ORDER BY todo_id"
        ).fetchall()
    return [row_to_todo(row) for row in rows]


@app.get(
    "/todos/{todo_id}",
    response_model=Todo,
    tags=["Todos"],
    summary="Get a todo by ID",
    operation_id="get_todo",
    responses={404: {"description": "Todo not found"}},
)
def get_todo(todo_id: int = TodoIdParam) -> Todo:
    """Return a single todo item by its ID."""
    with get_connection() as conn:
        row = fetch_todo_or_404(conn, todo_id)
    return row_to_todo(row)


@app.post(
    "/todos",
    response_model=Todo,
    status_code=status.HTTP_201_CREATED,
    tags=["Todos"],
    summary="Create a new todo",
    operation_id="create_todo",
)
def create_todo(todo: TodoCreate) -> Todo:
    """Add a new todo item and return it with its generated ID."""
    with get_connection() as conn:
        cursor = conn.execute(
            "INSERT INTO todos (content, completed) VALUES (?, ?)",
            (todo.content, int(todo.completed)),
        )
        new_id = cursor.lastrowid
    return Todo(todo_id=new_id, content=todo.content, completed=todo.completed)


@app.put(
    "/todos/{todo_id}",
    response_model=Todo,
    tags=["Todos"],
    summary="Update a todo",
    operation_id="update_todo",
    responses={404: {"description": "Todo not found"}},
)
def update_todo(todo_update: TodoUpdate, todo_id: int = TodoIdParam) -> Todo:
    """Update the content and/or completed status of an existing todo."""
    with get_connection() as conn:
        existing = row_to_todo(fetch_todo_or_404(conn, todo_id))

        # Keep current values for any fields not provided in the request
        content = (
            todo_update.content
            if todo_update.content is not None
            else existing.content
        )
        completed = (
            todo_update.completed
            if todo_update.completed is not None
            else existing.completed
        )

        conn.execute(
            "UPDATE todos SET content = ?, completed = ? WHERE todo_id = ?",
            (content, int(completed), todo_id),
        )
    return Todo(todo_id=todo_id, content=content, completed=completed)


@app.delete(
    "/todos/{todo_id}",
    response_model=Message,
    tags=["Todos"],
    summary="Delete a todo",
    operation_id="delete_todo",
    responses={404: {"description": "Todo not found"}},
)
def delete_todo(todo_id: int = TodoIdParam) -> Message:
    """Delete a todo item by its ID."""
    with get_connection() as conn:
        fetch_todo_or_404(conn, todo_id)
        conn.execute("DELETE FROM todos WHERE todo_id = ?", (todo_id,))
    return Message(message=f"Todo with id {todo_id} deleted")


# ---------------------------------------------------------------------------
# MCP server
# ---------------------------------------------------------------------------

# Expose the todo endpoints as MCP tools. This must run after all routes are
# defined, since fastapi-mcp builds the tools from the app's OpenAPI schema.
mcp = FastApiMCP(
    app,
    name="ToDo MCP",
    description="MCP tools for managing todo items.",
    include_tags=["Todos"],  # Skip the root welcome route
    describe_all_responses=True,  # Include the 404 response in tool docs
)
mcp.mount_http()  # Serves the MCP endpoint at /mcp


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    import uvicorn

    uvicorn.run("main:app", host="127.0.0.1", port=8000, reload=True)
