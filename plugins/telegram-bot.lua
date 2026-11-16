-- telegram-bot.lua — send a message to the Telegram chat you are browsing
-- and add the audio the bot replies with to the current playlist.
--
-- The core owns the MTProto session and the send/reply calls. This plugin
-- only draws the prompt and decides what to do with the reply, so any bot
-- that answers a text command with an audio document works without code
-- changes.
--
-- The action appears under "!" when the Telegram provider is open.
-- A plain text query keeps waiting for the audio after an interim
-- "downloading" reply. Slash commands are treated as menu commands and do not
-- wait for delayed audio; use a plain query if the bot sends the audio after a
-- slash-command reply.
--
-- The p:command("ask") form is for scripts and the MCP:
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

local busy = {}
local requestSeq = 0

local function is_busy(chat)
    local entry = busy[chat]
    if not entry then
        return false
    end
    if entry.untilTime <= os.time() then
        busy[chat] = nil
        return false
    end
    return true
end

local function reserve(chat)
    if is_busy(chat) then
        return nil
    end
    requestSeq = requestSeq + 1
    -- Longer than the 60s ask plus the 30s audio follow-up. A canceled
    -- command cannot run its cleanup, so the deadline is the safety net.
    busy[chat] = {token = requestSeq, untilTime = os.time() + 180}
    return requestSeq
end

local function finish(chat, token)
    local entry = busy[chat]
    if not entry then
        return
    end
    if token and entry.token ~= token then
        return
    end
    busy[chat] = nil
end

local function insert_audio(reply)
    if reply and reply.kind == "audio" and reply.track then
        cliamp.queue.insert(0, reply.track)
        cliamp.message("Added: " .. reply.track.title, 5)
        return true
    end
    return false
end

local function wait_audio(chat, afterID, token)
    cliamp.provider.call("telegram.reply", {
        chat = chat,
        after = afterID,
        timeout = 20,
        audio_only = true,
    }, {
        on_reply = function(reply, err)
            if not insert_audio(reply) then
                if err then
                    cliamp.message("Telegram: " .. tostring(err), 8)
                elseif reply and reply.kind == "text" and reply.text and reply.text ~= "" then
                    cliamp.message(reply.text, 8)
                else
                    cliamp.message("No audio yet. Ctrl+R to reload.", 5)
                end
            end
            finish(chat, token)
        end,
        timeout = 30,
    })
end

local function add_reply(chat, reply, err, waitForAudio, token)
    if not reply then
        cliamp.message("Telegram: " .. tostring(err or "no reply"), 8)
        finish(chat, token)
        return
    end
    if insert_audio(reply) then
        finish(chat, token)
        return
    end
    if reply.kind == "text" then
        cliamp.message(reply.text, 8)
        if waitForAudio and reply.message_id and reply.message_id > 0 then
            -- The bot often sends "Downloading..." first and the audio after
            -- WaitReply's grace period. Keep waiting for the audio.
            wait_audio(chat, reply.message_id, token)
        else
            finish(chat, token)
        end
        return
    end
    cliamp.message("No reply yet. Ctrl+R to reload.", 5)
    finish(chat, token)
end

p:action{
    key = "!",
    id = "send",
    label = "Send message to this chat",
    description = "Ask the bot for a track",
    provider = "Telegram",
    context = "chat",
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
                local token = reserve(chat)
                if not token then
                    cliamp.message("Wait for the bot to finish this chat first.", 6)
                    return
                end
                cliamp.message("Sent. Waiting for the bot…", 3)
                local waitForAudio = text:sub(1, 1) ~= "/"
                local ok, err = cliamp.provider.call("telegram.ask", {
                    chat = chat,
                    text = text,
                    timeout = 60,
                }, {
                    on_reply = function(reply, replyErr)
                        add_reply(chat, reply, replyErr, waitForAudio, token)
                    end,
                    timeout = 90,
                })
                if not ok then
                    finish(chat, token)
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
    local token = reserve(chat)
    if not token then
        return "busy: wait for the bot to finish this chat"
    end
    local reply, err = cliamp.provider.call("telegram.ask", {
        chat = chat,
        text = text,
        timeout = 60,
    })
    finish(chat, token)
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
