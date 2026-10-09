-- The work surface is gone, and with it the one setting that only it read.
-- Left in place the row would sit in every backup and settings export as a
-- knob that does nothing.
DELETE FROM settings WHERE key = 'chat.agent_max_rounds';
