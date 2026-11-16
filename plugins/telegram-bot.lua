-- telegram-bot.lua — send a message to the Telegram chat you are browsing
-- and add the audio the bot replies with to the current playlist.
--
-- The core owns the MTProto session and the send/reply calls. This plugin
-- only draws the prompt and decides what to do with the reply, so any bot
-- that answers a text command with an audio document works without code
-- changes.
--
-- The action appears under "!" when the Telegram provider is open. The
-- p:command("ask") form is for scripts and the MCP:
--
--   cliamp plugins call telegram-bot ask <chat-id> /search daft punk

local p = plugin.register({
    name = "telegram-bot",
    type = "hook",
    version = "0.1.0",
    description = "Send a message to a Telegram bot and add its audio to the playlist",
    permissions = {"ui", "provider", "provider-send", "control"},
})

local function current_chat()
    local ctx = cliamp.provider.current()
    if ctx.name ~= "Telegram" then
        return nil, nil
    end
    local chat = ctx.playlist_id
    local name = ctx.playlist_name
    if chat == "" then
        chat = ctx.selection_id
        name = ctx.selection_name
    end
    if chat == "" then
        return nil, nil
    end
    return chat, name
end

local function add_reply(reply, err)
    if not reply then
        cliamp.message("Telegram: " .. tostring(err or "no reply"), 8)
        return
    end
    if reply.kind == "audio" and reply.track then
        cliamp.queue.insert(0, reply.track)
        cliamp.message("Added: " .. reply.track.title, 5)
        return
    end
    if reply.kind == "text" then
        cliamp.message(reply.text, 8)
        return
    end
    cliamp.message("No reply yet. Ctrl+R to reload.", 5)
end

p:action{
    key = "!",
    id = "send",
    label = "Send message to this chat",
    description = "Ask the bot for a track",
    provider = "Telegram",
    on_submit = function()
        local chat, name = current_chat()
        if not chat then
            cliamp.message("Open a Telegram chat first.", 3)
            return
        end
        cliamp.ui.input{
            title = "Message → " .. (name or chat),
            placeholder = "/search ...",
            on_submit = function(text)
                cliamp.message("Sent. Waiting for the bot…", 3)
                local ok, err = cliamp.provider.call("telegram.ask", {
                    chat = chat,
                    text = text,
                    timeout = 60,
                }, {
                    on_reply = add_reply,
                    timeout = 90,
                })
                if not ok then
                    cliamp.message("Send failed: " .. tostring(err), 8)
                end
            end,
        }
    end,
}

p:command("commands", function(args)
    local chat = args[1]
    if not chat or chat == "" then
        return "usage: commands <chat-id>"
    end
    local res, err = cliamp.provider.call("telegram.commands", {chat = chat})
    if not res then
        return "error: " .. tostring(err)
    end
    local out = {}
    for _, command in ipairs(res.commands) do
        out[#out+1] = command.command .. " — " .. command.description
    end
    return table.concat(out, "\n")
end)

p:command("ask", function(args)
    local chat = args[1]
    local text = table.concat(args, " ", 2)
    if not chat or chat == "" or text == "" then
        return "usage: ask <chat-id> <message>"
    end
    local reply, err = cliamp.provider.call("telegram.ask", {
        chat = chat,
        text = text,
        timeout = 60,
    })
    if not reply then
        return "error: " .. tostring(err)
    end
    if reply.kind == "audio" and reply.track then
        return "audio: " .. reply.track.title
    end
    local out = {reply.kind .. ": " .. tostring(reply.text)}
    if reply.buttons then
        for i, button in ipairs(reply.buttons) do
            out[#out+1] = string.format("  %d. %s kind=%s data=%s", i, button.text, tostring(button.kind), tostring(button.data_b64 or button.url))
        end
    end
    return table.concat(out, "\n")
end)
