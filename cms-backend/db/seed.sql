-- =========================================================================
-- STAR NOVEL - 生产环境种子与初始数据 (Seed Data)
-- 包含：初始超管账号 (admin/admin123)、系统全局计费配置、默认充值模板、默认域名、精选示例小说与章节
-- =========================================================================

-- 1. 初始超级管理员 (admin / admin123)
INSERT INTO admins (id, username, password_hash, nickname, role, status) 
VALUES ('10000000-0000-0000-0000-000000000001', 'admin', '$2a$10$F18fKqJSG4r6zPEp3EokNesCxK005I2a66WHrWhwopOOBA./liWqa', '超级管理员', 'SuperAdmin', 1) 
ON CONFLICT (username) DO UPDATE SET password_hash = EXCLUDED.password_hash;

-- 2. 全局计费与付费章节门槛配置
INSERT INTO system_configs (key, value) VALUES ('global_coin_cost_per_thousand', '500') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
INSERT INTO system_configs (key, value) VALUES ('global_start_pay_chapter_index', '3') ON CONFLICT (key) DO NOTHING;

-- 3. 默认充值模板 (6卡位)
INSERT INTO recharge_templates (id, name, is_default) VALUES (1, '默认充值模板', TRUE) ON CONFLICT (id) DO NOTHING;

INSERT INTO recharge_slots (template_id, slot_index, type, coins, bonus, vip_duration, vip_name, vip_desc, price, price_cents)
VALUES
(1, 1, 'single', 499, 50, '', '', '', '$4.99', 499),
(1, 2, 'single', 999, 150, '', '', '', '$9.99', 999),
(1, 3, 'single', 1999, 400, '', '', '', '$19.99', 1999),
(1, 4, 'single', 4999, 1200, '', '', '', '$49.99', 4999),
(1, 5, 'vip', 299, 0, 'week', 'VIP Weekly', 'Get 299 Coins + 50/day', '$2.99', 299),
(1, 6, 'vip', 999, 0, 'month', 'VIP Monthly', 'Get 999 Coins + 80/day', '$9.99', 999)
ON CONFLICT (template_id, slot_index) DO NOTHING;

-- 4. 默认主站落地页域名
INSERT INTO system_domains (name, domain, type, status, is_default)
VALUES ('主站默认落地页', 'h5.star-novel.com', 'main', 1, TRUE)
ON CONFLICT (domain) DO NOTHING;

-- Mock Data Seeder for Star Novel

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000001,
  'The Bride They Replaced Became Heir',
  'Rola Rose',
  '/assets/the_bride_they_replaced_became_heir.png',
  4.5,
  'Ongoing',
  'A night before her wedding, Daisy White walks into a room filled with white orchids, soft music, and the people she trusts most… only to hear the words that shatter her world.
“Your stepsister is pregnant with your fiancé’s child. The wedding will still happen tomorrow, but Samantha will be the bride.”
Seven months.
That’s how long they laughed behind her back while she planned her future. Seven months of lies, betrayal and watching her build a life they were already stealing from her.
By midnight, Daisy loses her fiancé, her family, and the future she thought belonged to her.
Then at 1AM on her twenty-fifth birthday, a stranger sends her a message that changes everything:
Your mother never left your father her company.
She left it to you.
Suddenly, the unwanted daughter they cast aside becomes the hidden owner of the billion-dollar empire her father took after her mother’s mysterious death.
But as Daisy starts tearing her family’s perfect world apart, she uncovers a horrifying truth:
Her mother was never supposed to die.

The bride was replaced.
The heir was erased.

Now Daisy is coming back for everything they stole.',
  '{"Betrayal","Kickass Heroine","She-power","Revenge","Hidden Identity"}',
  47000,
  7800000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000001-ch1',
  10000001,
  0,
  'Chapter 1: The Shattered Promise',
  'It was supposed to be the happiest day of Daisy''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Daisy murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Daisy walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Daisy would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of The Bride They Replaced Became Heir was just beginning, and every choice would shape the destiny of Rola Rose''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000001-ch2',
  10000001,
  1,
  'Chapter 2: A New Path',
  'It was supposed to be the happiest day of Daisy''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Daisy murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Daisy walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Daisy would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of The Bride They Replaced Became Heir was just beginning, and every choice would shape the destiny of Rola Rose''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000001-ch3',
  10000001,
  2,
  'Chapter 3: Unexpected Alliance',
  'It was supposed to be the happiest day of Daisy''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Daisy murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Daisy walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Daisy would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of The Bride They Replaced Became Heir was just beginning, and every choice would shape the destiny of Rola Rose''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000001-ch4',
  10000001,
  3,
  'Chapter 4: The Unveiled Power',
  'It was supposed to be the happiest day of Daisy''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Daisy murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Daisy walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Daisy would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of The Bride They Replaced Became Heir was just beginning, and every choice would shape the destiny of Rola Rose''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000001-ch5',
  10000001,
  4,
  'Chapter 5: Rising from the Ashes',
  'It was supposed to be the happiest day of Daisy''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Daisy murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Daisy walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Daisy would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of The Bride They Replaced Became Heir was just beginning, and every choice would shape the destiny of Rola Rose''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000006,
  'Alpha Tristan Regretted Divorcing Me ',
  'Jimoh Omowumi',
  '/assets/alpha_tristan_regretted_divorcing_me_.png',
  4.6,
  'Ongoing',
  'YELENA:
I didn''t think twice when I accepted my parents’ offer to marry our Alpha. He was a man every she-wolf in our pack wanted… I thought marrying him would mean love, protection, and respect.

Instead, it meant betrayal.

Tristan Crosswood, my husband, the Alpha of Blue Moon pack never wanted me. I was just a Luna by contract, replacing his empty title. He chose his mate repeatedly when she found her way back to the pack while I was foolish to reject mine the first time I saw him. 

I was such a fool… and I suffered for it.
So, I did the only thing left for me to save myself.

I filed for a divorce, and Alpha Tristan signed the papers happily. I walked away broken but free, and when I started to heal, I discovered that I was carrying his pup.

Now Alpha Tristan wanted me back, the Alpha who signed my freedom with his own hands became the Alpha who regret divorcing me.

But what he didn''t know was that… I wasn''t the desperate Luna he discarded.',
  '{"Dominant","Second Chance","Alpha","Regret","Billionaire"}',
  421000,
  2000000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000006-ch1',
  10000006,
  0,
  'Chapter 1: The Shattered Promise',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Alpha stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Alpha.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Alpha had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Alpha''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Alpha; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Alpha turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of Alpha Tristan Regretted Divorcing Me  was just beginning, and every choice would shape the destiny of Jimoh Omowumi''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000006-ch2',
  10000006,
  1,
  'Chapter 2: A New Path',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Alpha stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Alpha.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Alpha had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Alpha''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Alpha; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Alpha turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of Alpha Tristan Regretted Divorcing Me  was just beginning, and every choice would shape the destiny of Jimoh Omowumi''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000006-ch3',
  10000006,
  2,
  'Chapter 3: Unexpected Alliance',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Alpha stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Alpha.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Alpha had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Alpha''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Alpha; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Alpha turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of Alpha Tristan Regretted Divorcing Me  was just beginning, and every choice would shape the destiny of Jimoh Omowumi''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000006-ch4',
  10000006,
  3,
  'Chapter 4: The Unveiled Power',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Alpha stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Alpha.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Alpha had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Alpha''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Alpha; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Alpha turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of Alpha Tristan Regretted Divorcing Me  was just beginning, and every choice would shape the destiny of Jimoh Omowumi''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000006-ch5',
  10000006,
  4,
  'Chapter 5: Rising from the Ashes',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Alpha stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Alpha.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Alpha had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Alpha''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Alpha; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Alpha turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of Alpha Tristan Regretted Divorcing Me  was just beginning, and every choice would shape the destiny of Jimoh Omowumi''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000007,
  'My Sister Stole My Wedding, So I Took Her Husband''s Company',
  'Rola Rose',
  '/assets/my_sister_stole_my_wedding__so_i_took_her_husband_s_company.png',
  4.7,
  'Completed',
  'One hour before her wedding, Elise Wright’s parents lock her in a room and replace her with the one person she trusted most, her younger sister.
As the wedding march plays, Elise walks away from the life she thought she wanted, betrayed by her family and the man who was supposed to love her.
What they never knew was that Elise wasn’t the powerless daughter they believed her to be.
Hidden in her grandfather’s will is a secret that changes everything. Elise is the majority shareholder of Celestial Holdings, a multi-billion-dollar tech empire… and the company funding her ex-fiancé’s entire business.
Now the man who betrayed her is begging for mercy, her sister’s perfect marriage is falling apart, and the family that chose the wrong daughter is finally learning the truth.
They took her wedding.
But Elise took the empire.',
  '{"Betrayal","Regret","She-power","Independent","Cheating"}',
  8000,
  13100000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000007-ch1',
  10000007,
  0,
  'Chapter 1: The Shattered Promise',
  'It was supposed to be the happiest day of One''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," One murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, One walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, One would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of My Sister Stole My Wedding, So I Took Her Husband''s Company was just beginning, and every choice would shape the destiny of Rola Rose''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000007-ch2',
  10000007,
  1,
  'Chapter 2: A New Path',
  'It was supposed to be the happiest day of One''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," One murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, One walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, One would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of My Sister Stole My Wedding, So I Took Her Husband''s Company was just beginning, and every choice would shape the destiny of Rola Rose''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000007-ch3',
  10000007,
  2,
  'Chapter 3: Unexpected Alliance',
  'It was supposed to be the happiest day of One''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," One murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, One walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, One would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of My Sister Stole My Wedding, So I Took Her Husband''s Company was just beginning, and every choice would shape the destiny of Rola Rose''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000007-ch4',
  10000007,
  3,
  'Chapter 4: The Unveiled Power',
  'It was supposed to be the happiest day of One''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," One murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, One walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, One would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of My Sister Stole My Wedding, So I Took Her Husband''s Company was just beginning, and every choice would shape the destiny of Rola Rose''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000007-ch5',
  10000007,
  4,
  'Chapter 5: Rising from the Ashes',
  'It was supposed to be the happiest day of One''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," One murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, One walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, One would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of My Sister Stole My Wedding, So I Took Her Husband''s Company was just beginning, and every choice would shape the destiny of Rola Rose''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000008,
  'His Dying Luna Became His Greatest Enemy',
  'Precious',
  '/assets/his_dying_luna_became_his_greatest_enemy.jpg',
  4.8,
  'Ongoing',
  'Seven months pregnant with twins, I caught my mate Alpha Damon kissing his mistress in the hospital corridor where I''d just received my lupus cancer diagnosis.

For months, I''ve endured his lies while he parades Sophia around our pack, claiming she''s just a vulnerable pack member who needs protection.

Their cruelest betrayal? When he gave her my sacred Luna ring and forced me to apologize after she deliberately injured herself to frame me.

When Damon makes me sign papers and kicks me out of our home so Sophia can sleep in the Luna''s chamber meant for my twins, I finally walk away.

What he never expected was for his dying, discarded Luna to disappear with his heirs—and return more powerful than he ever imagined.

Now I have a choice: die quietly in the shadows or make Damon Reed regret every moment he chose his mistress over his mate.

It''s payback time—and cancer won''t stop me from destroying the man who broke my heart.',
  '{"Betrayal","Werewolf","Mate","Pregancy","Romance"}',
  83000,
  18500000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000008-ch1',
  10000008,
  0,
  'Chapter 1: The Shattered Promise',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Seven stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Seven.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Seven had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Seven''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Seven; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Seven turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of His Dying Luna Became His Greatest Enemy was just beginning, and every choice would shape the destiny of Precious''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000008-ch2',
  10000008,
  1,
  'Chapter 2: A New Path',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Seven stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Seven.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Seven had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Seven''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Seven; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Seven turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of His Dying Luna Became His Greatest Enemy was just beginning, and every choice would shape the destiny of Precious''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000008-ch3',
  10000008,
  2,
  'Chapter 3: Unexpected Alliance',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Seven stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Seven.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Seven had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Seven''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Seven; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Seven turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of His Dying Luna Became His Greatest Enemy was just beginning, and every choice would shape the destiny of Precious''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000008-ch4',
  10000008,
  3,
  'Chapter 4: The Unveiled Power',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Seven stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Seven.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Seven had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Seven''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Seven; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Seven turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of His Dying Luna Became His Greatest Enemy was just beginning, and every choice would shape the destiny of Precious''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000008-ch5',
  10000008,
  4,
  'Chapter 5: Rising from the Ashes',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Seven stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Seven.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Seven had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Seven''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Seven; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Seven turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of His Dying Luna Became His Greatest Enemy was just beginning, and every choice would shape the destiny of Precious''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000009,
  'The Alpha Chose My Sister, So I Chose Revenge',
  'DIMSON',
  '/assets/the_alpha_chose_my_sister__so_i_chose_revenge.jpg',
  4.9,
  'Ongoing',
  'Chapter 1
"Celeste, your resignation has been approved, but... are you certain about this? Alpha Marcus seemed surprised when he signed it. Perhaps you should reconsider?"
Celeste kept her eyes fixed on the resignation letter, her fingers tracing the familiar signature at the bottom. She shook her head slowly. "No need to reconsider. He''s made his choice."
The HR manager shifted uncomfortably. "But Celeste, everyone knows how much the Alpha—"
"How much he what?" Celeste''s laugh was hollow. "Cares about me? Values me? Loves me?"
What the HR manager didn''t know was that Celeste wasn''t just another pack member leaving the company. She was Marcus Sterling''s mate—had been for four years. Their mating bond was a secret, buried beneath pack politics and his personal shame.
But love and mating were two very different things, as she''d learned six weeks ago.
The night she miscarried their first pup—alone in a hospital room at 2 AM, bleeding and terrified—she''d called him seventeen times through their mate bond. When he finally answered, his voice was thick with sleep and irritation.
"Celeste, it''s the middle of the night. Can''t this wait until morning?"
"Marcus, I''m losing the pup. I need you here. The doctors need consent for—"
"Look, I''m sure it''s just cramping. She-wolves go through this all the time. Take some painkillers and call me in the morning."
The line went dead, and with it, something in their mate bond snapped forever.
She''d signed her own consent forms while hemorrhaging, her hands shaking so badly she could barely hold the pen. Her wolf whimpered in pain, calling for her mate, but he never came.
When the doctor told her the pup was gone, she was still alone.
Marcus showed up three days later—not to comfort his grieving mate, but to drag her back to work because his assistant,  had accidentally deleted important files and he needed Celeste to recover them.
He had been with Raven. Raven. Her younger sister. The woman who''d been warming his bed while his mate was losing their pup.
"People change," Celeste said quietly to the HR manager. "I''ve given him four years of my life. That''s enough."
The irony wasn''t lost on her. Marcus had claimed her as his mate because she was useful—the brilliant daughter of a neighboring pack''s Beta who could make his company profitable. But he''d never wanted a true mate. He''d wanted a business partner who occasionally warmed his bed.
Now that Raven was back from her Europe, he had what he''d always really wanted.
Before she left, though, Celeste had one final gift for her Alpha. Something that would set them both free.',
  '{"Werewolf","Rejection","Cheating","Romance","Second Chance"}',
  75000,
  9800000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000009-ch1',
  10000009,
  0,
  'Chapter 1: The Shattered Promise',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Chapter stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Chapter.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Chapter had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Chapter''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Chapter; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Chapter turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of The Alpha Chose My Sister, So I Chose Revenge was just beginning, and every choice would shape the destiny of DIMSON''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000009-ch2',
  10000009,
  1,
  'Chapter 2: A New Path',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Chapter stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Chapter.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Chapter had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Chapter''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Chapter; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Chapter turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of The Alpha Chose My Sister, So I Chose Revenge was just beginning, and every choice would shape the destiny of DIMSON''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000009-ch3',
  10000009,
  2,
  'Chapter 3: Unexpected Alliance',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Chapter stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Chapter.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Chapter had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Chapter''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Chapter; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Chapter turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of The Alpha Chose My Sister, So I Chose Revenge was just beginning, and every choice would shape the destiny of DIMSON''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000009-ch4',
  10000009,
  3,
  'Chapter 4: The Unveiled Power',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Chapter stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Chapter.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Chapter had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Chapter''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Chapter; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Chapter turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of The Alpha Chose My Sister, So I Chose Revenge was just beginning, and every choice would shape the destiny of DIMSON''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000009-ch5',
  10000009,
  4,
  'Chapter 5: Rising from the Ashes',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Chapter stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Chapter.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Chapter had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Chapter''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Chapter; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Chapter turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of The Alpha Chose My Sister, So I Chose Revenge was just beginning, and every choice would shape the destiny of DIMSON''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000010,
  'My hand for her dream.',
  'Temi writes',
  '/assets/my_hand_for_her_dream_.jpeg',
  4.5,
  'Completed',
  'I thought my fiancé loved me, until the day I found out he ruined my hands so his first love could take everything I worked for.

So I said yes to another man.',
  '{"Betrayal","Confident","Heart Wrenching","Independent","Billionaire"}',
  22000,
  3400000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000010-ch1',
  10000010,
  0,
  'Chapter 1: The Shattered Promise',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. So held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but So had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, So felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of My hand for her dream. was just beginning, and every choice would shape the destiny of Temi writes''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000010-ch2',
  10000010,
  1,
  'Chapter 2: A New Path',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. So held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but So had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, So felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of My hand for her dream. was just beginning, and every choice would shape the destiny of Temi writes''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000010-ch3',
  10000010,
  2,
  'Chapter 3: Unexpected Alliance',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. So held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but So had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, So felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of My hand for her dream. was just beginning, and every choice would shape the destiny of Temi writes''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000010-ch4',
  10000010,
  3,
  'Chapter 4: The Unveiled Power',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. So held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but So had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, So felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of My hand for her dream. was just beginning, and every choice would shape the destiny of Temi writes''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000010-ch5',
  10000010,
  4,
  'Chapter 5: Rising from the Ashes',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. So held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but So had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, So felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of My hand for her dream. was just beginning, and every choice would shape the destiny of Temi writes''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000011,
  'Spoiled By My Overprotective Brothers ',
  'A Knight In Skirt',
  '/assets/spoiled_by_my_overprotective_brothers_.jpg',
  4.6,
  'Ongoing',
  'She thought marrying a powerful CEO would bring her happiness. Instead, Liana was neglected, humiliated, and treated as nothing more than a placeholder wife. When her husband openly brought his first love into their home, she finally snapped—throwing the divorce papers in his face and walking away without looking back.

Everyone thought she was ruined. But then came the shocking truth: Liana was the long-lost daughter of the influential Carver family.

Her three overprotective brothers appeared like a storm to shield her from the world:

Leo Carver, the ruthless business tycoon, handed her shares worth billions.

Cassian Carver, the sharpest lawyer in the country, swore her ex-husband would crawl out of the divorce with nothing.

Dante Carver, the nation’s beloved superstar, announced to millions: “She is my only sister. Whoever dares bully her will answer to me.”


From the ashes of betrayal, Liana rose brighter than ever, living like a queen under her brothers’ protection.

And when her ex-husband came crawling back, begging for another chance, her brothers only smirked coldly—

“Chasing after our sister? You’re not even worthy.”',
  '{"Confident","Kickass Heroine","Love Triangle","Billionaire","Revenge"}',
  245000,
  2400000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000011-ch1',
  10000011,
  0,
  'Chapter 1: The Shattered Promise',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Instead held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Instead had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Instead felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of Spoiled By My Overprotective Brothers  was just beginning, and every choice would shape the destiny of A Knight In Skirt''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000011-ch2',
  10000011,
  1,
  'Chapter 2: A New Path',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Instead held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Instead had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Instead felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of Spoiled By My Overprotective Brothers  was just beginning, and every choice would shape the destiny of A Knight In Skirt''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000011-ch3',
  10000011,
  2,
  'Chapter 3: Unexpected Alliance',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Instead held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Instead had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Instead felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of Spoiled By My Overprotective Brothers  was just beginning, and every choice would shape the destiny of A Knight In Skirt''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000011-ch4',
  10000011,
  3,
  'Chapter 4: The Unveiled Power',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Instead held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Instead had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Instead felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of Spoiled By My Overprotective Brothers  was just beginning, and every choice would shape the destiny of A Knight In Skirt''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000011-ch5',
  10000011,
  4,
  'Chapter 5: Rising from the Ashes',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Instead held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Instead had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Instead felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of Spoiled By My Overprotective Brothers  was just beginning, and every choice would shape the destiny of A Knight In Skirt''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000005,
  'The woman he destroyed, The mystery he’ll never solve',
  'JT Writes',
  '/assets/the_woman_he_destroyed__the_mystery_he_ll_never_solve.png',
  4.7,
  'Completed',
  'The day I caught my boyfriend Caleb at a motel with his student Mila, he told me he''d been cheating for six months.
On our fourth anniversary, which happened to fall one day after the Mexico travel ban lifted, I decided to disappear instead of staying for the dinner I''d planned.
When Caleb came home from the restaurant where he''d waited two hours for me, he found my phone on the couch, my keys by the door, and the promise ring he''d given me on the kitchen table.
"Elara?" he called as he stared at the shattered glass scattered across the kitchen floor.
But I wasn''t there to answer.
For four years, I''d endured his violence, his gaslighting, his public affair while he convinced me I was the problem. He''d slapped me in front of strangers. Pushed me into counters. Told me no one else would tolerate me.
He thought I was too broken to leave.
But I didn''t leave. I vanished.
I left behind planted evidence: a journal that read like a suicide note, blood on the kitchen floor, and treasure hunt clues leading investigators straight to his crimes.
That night, while he called my name in our empty apartment, I became someone else entirely.
When the investigation began and his world gradually unraveled, he would finally see what I had turned out to be: not his victim, but the architect of his ruin.',
  '{"Betrayal","Thriller","Romance","Cheating","Revenge"}',
  40000,
  9700000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000005-ch1',
  10000005,
  0,
  'Chapter 1: The Shattered Promise',
  'It was supposed to be the happiest day of Caleb''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Caleb murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Caleb walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Caleb would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of The woman he destroyed, The mystery he’ll never solve was just beginning, and every choice would shape the destiny of JT Writes''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000005-ch2',
  10000005,
  1,
  'Chapter 2: A New Path',
  'It was supposed to be the happiest day of Caleb''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Caleb murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Caleb walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Caleb would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of The woman he destroyed, The mystery he’ll never solve was just beginning, and every choice would shape the destiny of JT Writes''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000005-ch3',
  10000005,
  2,
  'Chapter 3: Unexpected Alliance',
  'It was supposed to be the happiest day of Caleb''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Caleb murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Caleb walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Caleb would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of The woman he destroyed, The mystery he’ll never solve was just beginning, and every choice would shape the destiny of JT Writes''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000005-ch4',
  10000005,
  3,
  'Chapter 4: The Unveiled Power',
  'It was supposed to be the happiest day of Caleb''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Caleb murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Caleb walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Caleb would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of The woman he destroyed, The mystery he’ll never solve was just beginning, and every choice would shape the destiny of JT Writes''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000005-ch5',
  10000005,
  4,
  'Chapter 5: Rising from the Ashes',
  'It was supposed to be the happiest day of Caleb''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Caleb murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Caleb walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Caleb would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of The woman he destroyed, The mystery he’ll never solve was just beginning, and every choice would shape the destiny of JT Writes''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000002,
  'Married Twice Loved Once',
  'Amyoga',
  '/assets/married_twice_loved_once.jpg',
  4.8,
  'Ongoing',
  'Aria Carter died betrayed.
Her husband ignored her.
Her best friend stabbed her in the back.
Her family sold her off like a pawn.

When she opened her eyes again three years earlier, on the night of her arranged marriage to the city’s coldest CEO she swore this life would be different.

No more weakness.
No more blind love.
No more kneeling.

Damian Cross, the ruthless billionaire everyone fears, expected a docile wife to decorate his mansion. Instead, he got a woman who met his icy stare with fire of her own.

Society sneers at her as the “Cold Wife.”
Her family calls her a disgrace.
Her enemies plot her downfall.

But this time, Aria isn’t here to beg for scraps she’s here to flip the board.

Every betrayal will be repaid.
Every secret will be exposed.
And the husband who once ignored her?
He’s falling, dangerously, obsessively, in love.

Yet beneath the glittering empire lies the truth of her first death…
and if Aria isn’t careful, the crown she claims may cost her heart all over again.',
  '{"Betrayal","Second Chance","Reborn","Billionaire","Revenge"}',
  213000,
  7400000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000002-ch1',
  10000002,
  0,
  'Chapter 1: The Shattered Promise',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Aria held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Aria had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Aria felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of Married Twice Loved Once was just beginning, and every choice would shape the destiny of Amyoga''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000002-ch2',
  10000002,
  1,
  'Chapter 2: A New Path',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Aria held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Aria had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Aria felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of Married Twice Loved Once was just beginning, and every choice would shape the destiny of Amyoga''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000002-ch3',
  10000002,
  2,
  'Chapter 3: Unexpected Alliance',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Aria held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Aria had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Aria felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of Married Twice Loved Once was just beginning, and every choice would shape the destiny of Amyoga''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000002-ch4',
  10000002,
  3,
  'Chapter 4: The Unveiled Power',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Aria held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Aria had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Aria felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of Married Twice Loved Once was just beginning, and every choice would shape the destiny of Amyoga''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000002-ch5',
  10000002,
  4,
  'Chapter 5: Rising from the Ashes',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Aria held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Aria had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Aria felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of Married Twice Loved Once was just beginning, and every choice would shape the destiny of Amyoga''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000012,
  'TWISTED OBSESSION: THE BILLIONAIRE''S VIRGIN BRIDE',
  'FLOATING INK',
  '/assets/twisted_obsession__the_billionaire_s_virgin_bride.png',
  4.9,
  'Ongoing',
  '"Strip for me," he commanded, his ice-blue eyes devouring her. "Tonight, I take what''s mine."

I thought I knew betrayal when I caught my fiancé''s balls-deep in another woman.
I thought I''d hit rock bottom when that woman turned out to be my TWIN SISTER.
But the real knife in my back? He was her boyfriend first. For eight months, she fed him every secret about me—my fears, my dreams, my insecurities—so he could seduce me, make me fall in love, and destroy me when it hurt the most.
Why? Because she''s always hated that I''m successful. That I''m respected. That people love me for who I am, not what I look like.
So there I was—promoted, engaged, happy—walking into my fiancé''s apartment to celebrate... only to find him f*cking my sister against the wall while she smirked at me over his shoulder.
"Surprise, sister. He was always mine."
I should''ve gone home. I should''ve cried. Should''ve been the victim they wanted me to be.
Instead, I walked into the most exclusive bar in New York and caught the attention of the most dangerous man in the city.
Dante Ashford.
Billionaire. My ex-fiancé''s boss. Sex personified. And currently getting pleasured by a beautiful blonde in the VIP lounge—while staring directly at ME.
He said my name while another woman''s mouth was on him. 
Then he dismissed her with one cold word: "Leave."
When I demanded to know how he knew who I was, he smiled like the devil making a deal.
"I know everything about you. Your cheating fiancé and backstabbing sister. I know exactly how to make them pay."
His offer was simple: Marry me. Destroy them. I''ll give you power, wealth, and revenge. 
My response shocked us both: "I''ll marry you. But first... take my virginity. Tonight."',
  '{"Betrayal","Billionaire","Erotic"}',
  156000,
  1400000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000012-ch1',
  10000012,
  0,
  'Chapter 1: The Shattered Promise',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Strip held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Strip had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Strip felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of TWISTED OBSESSION: THE BILLIONAIRE''S VIRGIN BRIDE was just beginning, and every choice would shape the destiny of FLOATING INK''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000012-ch2',
  10000012,
  1,
  'Chapter 2: A New Path',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Strip held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Strip had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Strip felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of TWISTED OBSESSION: THE BILLIONAIRE''S VIRGIN BRIDE was just beginning, and every choice would shape the destiny of FLOATING INK''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000012-ch3',
  10000012,
  2,
  'Chapter 3: Unexpected Alliance',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Strip held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Strip had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Strip felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of TWISTED OBSESSION: THE BILLIONAIRE''S VIRGIN BRIDE was just beginning, and every choice would shape the destiny of FLOATING INK''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000012-ch4',
  10000012,
  3,
  'Chapter 4: The Unveiled Power',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Strip held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Strip had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Strip felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of TWISTED OBSESSION: THE BILLIONAIRE''S VIRGIN BRIDE was just beginning, and every choice would shape the destiny of FLOATING INK''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000012-ch5',
  10000012,
  4,
  'Chapter 5: Rising from the Ashes',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Strip held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Strip had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Strip felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of TWISTED OBSESSION: THE BILLIONAIRE''S VIRGIN BRIDE was just beginning, and every choice would shape the destiny of FLOATING INK''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000013,
  'Confessions While the Stars Are Still Out',
  '新知网络内容平台',
  '/assets/confessions_while_the_stars_are_still_out.png',
  4.5,
  'Ongoing',
  'Three days before the wedding, my sister Mary’s laptop broke.

Desperate to finish editing a slideshow of childhood photos for my wedding, she borrowed my computer. That night, after she’d left, I went to close her QQ chat window when a message popped up.

It was from someone named “Adam”:

“Mary, still awake? You looked so beautiful trying on the wedding dress today. I couldn’t take my eyes off you.”

My fiancé’s name is Adam.

Rationally, I knew a man engaged to one sister wouldn’t send ambiguous messages to the other late at night. Yet my hands trembled as I opened their chat history.

Empty. Wiped clean, obviously, after every conversation.

Compelled, I didn’t close Mary’s QQ. Instead, I clicked into her space.

There was a group labeled “Visible to One Person Only.”

Shaking, I clicked in. That one person was Adam’s alternate account.

The latest post was from three hours ago—a photo of Mary in her bridesmaid dress.

The caption read: “They say I look better in this than the bride. Adam, what do you think? I really wish, one day, I could wear a real wedding dress for you.”

Below, Adam’s only comment: “In my heart, you look more beautiful than her in anything. Just wait a little longer, Mary.”

Scrolling further back, post after post, dense and endless, spanning six whole years.

Adam and I had been together for exactly six years.

And they had been entangled for six years.

Through it all.

I was drowning, forcing myself to read on, each word a fresh dose of poison.

The first post was from six years ago, in autumn, just one month after Adam and I had officially become a couple.

“Today, my sister brought her boyfriend home. That guy named Adam—when he smiles, his eyes sparkle like stars. What do I do… I think I’m falling for him too.”

The second post, after I brought Adam to a family gathering.

“He peeled a shrimp for me. My sister saw and joked that he was playing favorites. He laughed and said, ‘Mary’s the little sister, it’s only right.’ But I caught the flicker of tenderness in his eyes. That wasn’t a look for a sister. I know… I’m different to him too.”

A wave of nausea hit me.

I remembered that gathering. I’d even teased Adam for being so focused on peeling shrimp for Mary. He’d patted my head and said, “Babe, Mary’s your favorite little sister. If I’m not good to her, you’d be upset.”

Back then, I was utterly moved. I’d thought he loved everything connected to me—that he was a good man I could rely on.

Turns out, it wasn’t love for all that was mine. It was an impulse he couldn’t control.

Further down, the evidence showed their relationship progressing rapidly.

During our second year of university, I was swamped preparing materials for a graduate school recommendation. Adam said he’d come to the library with me, but he was never there. When I asked, he claimed the boys’ dorm was too noisy, so he’d found a quiet study room.

But in Mary’s space, she’d written: “Meeting Adam every day at our usual spot on the third-floor corner, then studying together, is my happiest time. He brings me warm milk, helps me with calculus. Watching his serious profile, I wish time would just stop here. Sister, I’m sorry. I really can’t control myself.”

Can’t control herself?

I couldn’t hold back a cold, bitter laugh.

I remembered how she’d come to me back then, all earnest concern, asking, “Sis, Adam is really such a great guy. You better keep a close eye on him. Don’t let some other woman steal him away.”

Looking back now, *she* was the one wearing the family mask—the deepest-hidden “other woman.”

But what shattered me completely was the incident where I gave up my spot in the master’s program.

I’d already been accepted into our university’s master’s program. But Adam was desperate to start his business. He pulled me into an all-night talk, hoping I’d join him in the struggle, be his most solid support.',
  '{"Second Chance","Regret","She-power","Billionaire","Romance"}',
  7000,
  922000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000013-ch1',
  10000013,
  0,
  'Chapter 1: The Shattered Promise',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Three held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Three had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Three felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of Confessions While the Stars Are Still Out was just beginning, and every choice would shape the destiny of 新知网络内容平台''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000013-ch2',
  10000013,
  1,
  'Chapter 2: A New Path',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Three held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Three had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Three felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of Confessions While the Stars Are Still Out was just beginning, and every choice would shape the destiny of 新知网络内容平台''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000013-ch3',
  10000013,
  2,
  'Chapter 3: Unexpected Alliance',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Three held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Three had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Three felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of Confessions While the Stars Are Still Out was just beginning, and every choice would shape the destiny of 新知网络内容平台''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000013-ch4',
  10000013,
  3,
  'Chapter 4: The Unveiled Power',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Three held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Three had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Three felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of Confessions While the Stars Are Still Out was just beginning, and every choice would shape the destiny of 新知网络内容平台''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000013-ch5',
  10000013,
  4,
  'Chapter 5: Rising from the Ashes',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Three held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Three had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Three felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of Confessions While the Stars Are Still Out was just beginning, and every choice would shape the destiny of 新知网络内容平台''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000004,
  'After the Divorce, My Ex-husband came Crawling Back',
  'Tamara Knox',
  '/assets/after_the_divorce__my_ex_husband_came_crawling_back.jpg',
  4.6,
  'Ongoing',
  'For three years, Ariana Grace Chase played the role of a wife who was never truly chosen.
Their marriage was a contract.
His heart belonged to another woman.
And when his first love returned, Maxwell Cox handed Ariana divorce papers without hesitation.
He thought money would erase her.
He thought she would beg.
Instead, Ariana walked away, with his assets, his power, and the inheritance he never knew he could lose.
After the divorce, Maxwell realizes too late that the woman he discarded now controls everything he was raised to inherit. 
Pregnant, untouchable, and finally free, Ariana disappears from his world only to return as the woman he can no longer reach.
As secrets unravel, families collapse, and bloodlines are exposed, Maxwell’s regret turns into obsession. 
He wants his ex-wife back. His empire back. His legacy back.
But some women are only disposable once.
And when a man comes crawling back after the divorce, he may find the door permanently closed.',
  '{"Second Chance","Twins","She-power","Friends to Lovers","Romance"}',
  120000,
  6600000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000004-ch1',
  10000004,
  0,
  'Chapter 1: The Shattered Promise',
  'The city lights blurred into long streaks of neon gold and silver through the window of the taxi. For leaned against the cold glass, feeling the heavy exhaustion of the past year. Everything had changed so fast, leaving no time to heal.

But life in the city had its own rhythm, and For was not ready to give up. The dream of starting over, away from the lies and shadows of the past, was closer than ever. All it took was a single step of courage.

Suddenly, the taxi screeched to a halt. A sleek black sports car blocked the lane, and a man in a dark coat stepped out, his gaze locked onto the cab. For''s heart skipped a beat. He had found For again.

"We need to talk," he said, his voice cut through the noise of the rain. The secrets of the past could no longer be hidden, and For had to stand and face the storm.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of After the Divorce, My Ex-husband came Crawling Back was just beginning, and every choice would shape the destiny of Tamara Knox''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000004-ch2',
  10000004,
  1,
  'Chapter 2: A New Path',
  'The city lights blurred into long streaks of neon gold and silver through the window of the taxi. For leaned against the cold glass, feeling the heavy exhaustion of the past year. Everything had changed so fast, leaving no time to heal.

But life in the city had its own rhythm, and For was not ready to give up. The dream of starting over, away from the lies and shadows of the past, was closer than ever. All it took was a single step of courage.

Suddenly, the taxi screeched to a halt. A sleek black sports car blocked the lane, and a man in a dark coat stepped out, his gaze locked onto the cab. For''s heart skipped a beat. He had found For again.

"We need to talk," he said, his voice cut through the noise of the rain. The secrets of the past could no longer be hidden, and For had to stand and face the storm.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of After the Divorce, My Ex-husband came Crawling Back was just beginning, and every choice would shape the destiny of Tamara Knox''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000004-ch3',
  10000004,
  2,
  'Chapter 3: Unexpected Alliance',
  'The city lights blurred into long streaks of neon gold and silver through the window of the taxi. For leaned against the cold glass, feeling the heavy exhaustion of the past year. Everything had changed so fast, leaving no time to heal.

But life in the city had its own rhythm, and For was not ready to give up. The dream of starting over, away from the lies and shadows of the past, was closer than ever. All it took was a single step of courage.

Suddenly, the taxi screeched to a halt. A sleek black sports car blocked the lane, and a man in a dark coat stepped out, his gaze locked onto the cab. For''s heart skipped a beat. He had found For again.

"We need to talk," he said, his voice cut through the noise of the rain. The secrets of the past could no longer be hidden, and For had to stand and face the storm.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of After the Divorce, My Ex-husband came Crawling Back was just beginning, and every choice would shape the destiny of Tamara Knox''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000004-ch4',
  10000004,
  3,
  'Chapter 4: The Unveiled Power',
  'The city lights blurred into long streaks of neon gold and silver through the window of the taxi. For leaned against the cold glass, feeling the heavy exhaustion of the past year. Everything had changed so fast, leaving no time to heal.

But life in the city had its own rhythm, and For was not ready to give up. The dream of starting over, away from the lies and shadows of the past, was closer than ever. All it took was a single step of courage.

Suddenly, the taxi screeched to a halt. A sleek black sports car blocked the lane, and a man in a dark coat stepped out, his gaze locked onto the cab. For''s heart skipped a beat. He had found For again.

"We need to talk," he said, his voice cut through the noise of the rain. The secrets of the past could no longer be hidden, and For had to stand and face the storm.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of After the Divorce, My Ex-husband came Crawling Back was just beginning, and every choice would shape the destiny of Tamara Knox''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000004-ch5',
  10000004,
  4,
  'Chapter 5: Rising from the Ashes',
  'The city lights blurred into long streaks of neon gold and silver through the window of the taxi. For leaned against the cold glass, feeling the heavy exhaustion of the past year. Everything had changed so fast, leaving no time to heal.

But life in the city had its own rhythm, and For was not ready to give up. The dream of starting over, away from the lies and shadows of the past, was closer than ever. All it took was a single step of courage.

Suddenly, the taxi screeched to a halt. A sleek black sports car blocked the lane, and a man in a dark coat stepped out, his gaze locked onto the cab. For''s heart skipped a beat. He had found For again.

"We need to talk," he said, his voice cut through the noise of the rain. The secrets of the past could no longer be hidden, and For had to stand and face the storm.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of After the Divorce, My Ex-husband came Crawling Back was just beginning, and every choice would shape the destiny of Tamara Knox''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000003,
  'Stay Away Ethan, Not Yours Anymore.',
  'Z_S_heaven',
  '/assets/stay_away_ethan__not_yours_anymore_.jpg',
  4.7,
  'Completed',
  '*They called me a wife. But treated me like a ghost.*

I cooked. I cleaned. I stayed silent.
For years, I folded myself small just to fit into their perfect little world.
Until one dinner shattered it all.

A child’s innocent wish. A cruel accusation.
And a betrayal so deep, it cracked something in me that would never heal.

When the man who vowed to protect me raised his hand instead…
I knew—I had to go.

So I took the card he tossed at me like a bone thrown to a dog…
And I vanished.

Now his calls won’t stop.
But I know why.
It’s not love he misses.
It’s his maid. His cleaner. His obedient, broken doll.

Too late, Ethan.
I’m not yours anymore.',
  '{"Heart Wrenching","Devil Husband","Billionaire","Revenge"}',
  63000,
  8600000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000003-ch1',
  10000003,
  0,
  'Chapter 1: The Shattered Promise',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. For held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but For had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, For felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of Stay Away Ethan, Not Yours Anymore. was just beginning, and every choice would shape the destiny of Z_S_heaven''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000003-ch2',
  10000003,
  1,
  'Chapter 2: A New Path',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. For held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but For had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, For felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of Stay Away Ethan, Not Yours Anymore. was just beginning, and every choice would shape the destiny of Z_S_heaven''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000003-ch3',
  10000003,
  2,
  'Chapter 3: Unexpected Alliance',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. For held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but For had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, For felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of Stay Away Ethan, Not Yours Anymore. was just beginning, and every choice would shape the destiny of Z_S_heaven''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000003-ch4',
  10000003,
  3,
  'Chapter 4: The Unveiled Power',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. For held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but For had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, For felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of Stay Away Ethan, Not Yours Anymore. was just beginning, and every choice would shape the destiny of Z_S_heaven''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000003-ch5',
  10000003,
  4,
  'Chapter 5: Rising from the Ashes',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. For held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but For had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, For felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of Stay Away Ethan, Not Yours Anymore. was just beginning, and every choice would shape the destiny of Z_S_heaven''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000014,
  'Ashes of Betrayal, Flames of Revenge',
  'Penleak',
  '/assets/ashes_of_betrayal__flames_of_revenge.png',
  4.8,
  'Ongoing',
  'Seven years. That''s how long Viola gave Brian everything: her dreams, her career, her whole future. All for a man who promised to be her family after she lost her parents.
One promotion to Paris. One shattered coffee table. One spilled urn of her parents'' ashes. That''s all it took for Viola to discover the truth: her fiancé had been living a double life with his dead best friend''s sister, Vanessa.
While Viola bled from glass wounds, Brian chose his lover over his bride-to-be. While she mourned her parents'' remains scattered across a dirty elevator floor, he was planning a secret wedding. While she stood humiliated in a wine-stained dress at Vanessa''s birthday party, they were already picking out wedding invitations.
But Brian made one crucial mistake—he underestimated the woman he destroyed.
Now she''s on a plane to Paris, and Brian has no idea what''s coming for him. The broken woman he destroyed is gone. What''s left is someone with nothing to lose and a burning need for payback.
He stole seven years of her life.
Time to take everything from his.
Or not.',
  '{"Betrayal","She-power","Billionaire","Cheating","Revenge"}',
  36000,
  10900000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000014-ch1',
  10000014,
  0,
  'Chapter 1: The Shattered Promise',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Seven held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Seven had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Seven felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of Ashes of Betrayal, Flames of Revenge was just beginning, and every choice would shape the destiny of Penleak''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000014-ch2',
  10000014,
  1,
  'Chapter 2: A New Path',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Seven held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Seven had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Seven felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of Ashes of Betrayal, Flames of Revenge was just beginning, and every choice would shape the destiny of Penleak''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000014-ch3',
  10000014,
  2,
  'Chapter 3: Unexpected Alliance',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Seven held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Seven had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Seven felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of Ashes of Betrayal, Flames of Revenge was just beginning, and every choice would shape the destiny of Penleak''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000014-ch4',
  10000014,
  3,
  'Chapter 4: The Unveiled Power',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Seven held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Seven had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Seven felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of Ashes of Betrayal, Flames of Revenge was just beginning, and every choice would shape the destiny of Penleak''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000014-ch5',
  10000014,
  4,
  'Chapter 5: Rising from the Ashes',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Seven held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Seven had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Seven felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of Ashes of Betrayal, Flames of Revenge was just beginning, and every choice would shape the destiny of Penleak''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000015,
  'The billionaire''s secret vow ',
  'EvieLuxe',
  '/assets/the_billionaire_s_secret_vow_.jpg',
  4.9,
  'Ongoing',
  'She married him to save her life. He married her to ruin hers.

Aria Monroe never dreamed of marrying a billionaire — especially not Damian Voss, the cold-hearted tycoon with a reputation as dark as his past. But when a medical crisis threatens Aria’s grandmother''s life and her flower shop is one rent payment from closing, she signs a contract marriage in exchange for the funds to survive.

Damian doesn’t believe in love — only leverage. And Aria? He’s convinced she’s the girl who destroyed his sister’s life months ago. Their marriage is a means to an end: revenge.

But the deeper Aria is pulled into his twisted world of wealth, secrets, and scars, the more Damian realizes she might not be the villain after all…
She might be the cure to the wounds he swore would never heal.

Until a shocking revelation tears their fragile bond apart—and an unexpected twin from Aria’s past reappears, threatening everything they thought they knew.',
  '{"Pregancy","Billionaire","Romance","Revenge"}',
  141000,
  1200000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000015-ch1',
  10000015,
  0,
  'Chapter 1: The Shattered Promise',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Aria held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Aria had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Aria felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of The billionaire''s secret vow  was just beginning, and every choice would shape the destiny of EvieLuxe''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000015-ch2',
  10000015,
  1,
  'Chapter 2: A New Path',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Aria held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Aria had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Aria felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of The billionaire''s secret vow  was just beginning, and every choice would shape the destiny of EvieLuxe''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000015-ch3',
  10000015,
  2,
  'Chapter 3: Unexpected Alliance',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Aria held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Aria had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Aria felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of The billionaire''s secret vow  was just beginning, and every choice would shape the destiny of EvieLuxe''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000015-ch4',
  10000015,
  3,
  'Chapter 4: The Unveiled Power',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Aria held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Aria had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Aria felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of The billionaire''s secret vow  was just beginning, and every choice would shape the destiny of EvieLuxe''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000015-ch5',
  10000015,
  4,
  'Chapter 5: Rising from the Ashes',
  'The rain beat heavily against the glass windows of the high-rise office in downtown. Aria held the contract in trembling hands, staring at the bold print: *Contract Marriage Agreement*. Across the glass desk sat the city''s most feared billionaire, his eyes dark and expressionless.

"Fifty million dollars will be wired to your account the moment you sign," his voice was smooth, cold, and carrying the authority of a ruler. "In public, we are the perfect couple. In private, we are strangers. Do not cross the line, and you will get everything you want."

It was a deal with the devil, but Aria had no choice. The family business was ruined, and the stepsister had already stolen the fiancé. Signing this paper was the only way to survive and make them pay for their betrayal.

As the pen scratched across the signature line, Aria felt a strange shift in the room. The cold billionaire leaned forward, a subtle, possessive gleam passing through his eyes. This was not just a business deal anymore; a game of hearts had begun.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of The billionaire''s secret vow  was just beginning, and every choice would shape the destiny of EvieLuxe''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000016,
  'The Triplets Alphas And Their Exclusive Doctor ',
  'Favourite',
  '/assets/the_triplets_alphas_and_their_exclusive_doctor_.jpg',
  4.5,
  'Ongoing',
  '"Tell me your observations, Alpha Azriel," I asked, not daring to look into those eyes—I might drown in them, especially since my mind couldn''t think of anything other than his length.
 I''m curious how it would feel to be taken by them all. Gosh, I''ve completely lost it—this is not me.
He exhaled lazily. "She is the one getting pleasure, not us."
My gaze shifted toward the others, and they nodded in agreement.
It made sense; she had been the only one reacting.
Then it hit me.
Axel had reacted.
I turned to him. “Axel, you’re already improving. That means you won''t be participating in the treatment anymore," I said with a small smile.
His brows tightened in irritation. “I’m not healed, Doctor… I still have difficulty getting my release.”
I rolled my eyes. "Cut the crap, Axel. You reacted during the session—that’s a clear sign of improvement." 
He let out a soft chuckle “The reason I reacted…” his gaze locked on mine, “…was because I was imagining you in her place.”
****
Alessia, a Luna and skilled doctor, is trapped in a humiliating marriage to her  Alpha mate. 
Her life takes a turn when she’s assigned to treat a VIP patients with a sensitive condition—only to discover her patients are the ruthless triplet Alphas from a rival pack. Their vulgar behavior pushes her to her limits.
When she tries to quit, they refuse, claiming she knows too much.
 Instead, they offer a deal: be their exclusive doctor for one month, and they’ll disappear from her life forever.
Desperate, she agrees—unaware that she has just signed a deal that would destroy everything she knew as a virtuous wife.
What dark secrets are the triplets truly hiding?
 what will become of Alessia when she uncovers them?',
  '{"BDSM","Harem","Triplets","Werewolf","Cheating"}',
  119000,
  636000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000016-ch1',
  10000016,
  0,
  'Chapter 1: The Shattered Promise',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Tell stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Tell.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Tell had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Tell''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Tell; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Tell turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of The Triplets Alphas And Their Exclusive Doctor  was just beginning, and every choice would shape the destiny of Favourite''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000016-ch2',
  10000016,
  1,
  'Chapter 2: A New Path',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Tell stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Tell.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Tell had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Tell''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Tell; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Tell turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of The Triplets Alphas And Their Exclusive Doctor  was just beginning, and every choice would shape the destiny of Favourite''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000016-ch3',
  10000016,
  2,
  'Chapter 3: Unexpected Alliance',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Tell stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Tell.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Tell had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Tell''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Tell; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Tell turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of The Triplets Alphas And Their Exclusive Doctor  was just beginning, and every choice would shape the destiny of Favourite''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000016-ch4',
  10000016,
  3,
  'Chapter 4: The Unveiled Power',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Tell stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Tell.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Tell had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Tell''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Tell; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Tell turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of The Triplets Alphas And Their Exclusive Doctor  was just beginning, and every choice would shape the destiny of Favourite''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000016-ch5',
  10000016,
  4,
  'Chapter 5: Rising from the Ashes',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Tell stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Tell.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Tell had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Tell''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Tell; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Tell turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of The Triplets Alphas And Their Exclusive Doctor  was just beginning, and every choice would shape the destiny of Favourite''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000017,
  'I was an Angel, You made me a Villain',
  'R.F EWELE',
  '/assets/i_was_an_angel__you_made_me_a_villain.jpg',
  4.6,
  'Completed',
  'He repayed with evil, I show him to hell',
  '{"She-power","Independent","Romance","Getting Back Together","Cheating"}',
  15000,
  12100000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000017-ch1',
  10000017,
  0,
  'Chapter 1: The Shattered Promise',
  'It was supposed to be the happiest day of the protagonist''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," the protagonist murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, the protagonist walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, the protagonist would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of I was an Angel, You made me a Villain was just beginning, and every choice would shape the destiny of R.F EWELE''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000017-ch2',
  10000017,
  1,
  'Chapter 2: A New Path',
  'It was supposed to be the happiest day of the protagonist''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," the protagonist murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, the protagonist walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, the protagonist would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of I was an Angel, You made me a Villain was just beginning, and every choice would shape the destiny of R.F EWELE''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000017-ch3',
  10000017,
  2,
  'Chapter 3: Unexpected Alliance',
  'It was supposed to be the happiest day of the protagonist''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," the protagonist murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, the protagonist walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, the protagonist would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of I was an Angel, You made me a Villain was just beginning, and every choice would shape the destiny of R.F EWELE''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000017-ch4',
  10000017,
  3,
  'Chapter 4: The Unveiled Power',
  'It was supposed to be the happiest day of the protagonist''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," the protagonist murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, the protagonist walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, the protagonist would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of I was an Angel, You made me a Villain was just beginning, and every choice would shape the destiny of R.F EWELE''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000017-ch5',
  10000017,
  4,
  'Chapter 5: Rising from the Ashes',
  'It was supposed to be the happiest day of the protagonist''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," the protagonist murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, the protagonist walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, the protagonist would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of I was an Angel, You made me a Villain was just beginning, and every choice would shape the destiny of R.F EWELE''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000018,
  'My Alpha''s Retribution',
  'Temi writes',
  '/assets/my_alpha_s_retribution.jpg',
  4.7,
  'Completed',
  'I used to believe love could tame an Alpha.

I was wrong.

The night I found my husband—my Alpha—between the legs of his secretary on our kitchen island, something inside me died. His words cut deeper than claws. He said I’d become too old, that he’d been waiting to get rid of me.

Three years of devotion. Three years of lies.

Now, I’m done being the obedient Luna everyone pities.',
  '{"Second Chance","Regret","Mate","Werewolf","Romance"}',
  19000,
  2400000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000018-ch1',
  10000018,
  0,
  'Chapter 1: The Shattered Promise',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Alpha stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Alpha.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Alpha had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Alpha''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Alpha; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Alpha turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of My Alpha''s Retribution was just beginning, and every choice would shape the destiny of Temi writes''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000018-ch2',
  10000018,
  1,
  'Chapter 2: A New Path',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Alpha stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Alpha.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Alpha had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Alpha''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Alpha; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Alpha turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of My Alpha''s Retribution was just beginning, and every choice would shape the destiny of Temi writes''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000018-ch3',
  10000018,
  2,
  'Chapter 3: Unexpected Alliance',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Alpha stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Alpha.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Alpha had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Alpha''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Alpha; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Alpha turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of My Alpha''s Retribution was just beginning, and every choice would shape the destiny of Temi writes''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000018-ch4',
  10000018,
  3,
  'Chapter 4: The Unveiled Power',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Alpha stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Alpha.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Alpha had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Alpha''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Alpha; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Alpha turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of My Alpha''s Retribution was just beginning, and every choice would shape the destiny of Temi writes''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000018-ch5',
  10000018,
  4,
  'Chapter 5: Rising from the Ashes',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Alpha stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Alpha.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Alpha had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Alpha''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Alpha; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Alpha turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of My Alpha''s Retribution was just beginning, and every choice would shape the destiny of Temi writes''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000019,
  'Tangled With The Other Brother',
  'Nyx Rae',
  '/assets/tangled_with_the_other_brother.webp',
  4.8,
  'Ongoing',
  'She was always the good girl. Until heartbreak made her reckless.

Elena Sinclair thought marriage meant forever.
But five years in, her “forever” has become a gilded cage of pain and betrayal.

She’s the wife who couldn’t give him a child. The barren disappointment. And for that, her husband offered her a cruel compromise… an open marriage. One that gave him the right to find someone who could carry his heir.

The next day, he brought a pregnant woman home. The same woman who was first introduced as his cousin.

The humiliation doesn’t end there, his mother lashes out… hurling insults and even fists, while her husband turns a blind eye. Not once has he defended her. Not once has he shown her love. She’s nothing more than a placeholder… a name on a marriage certificate.

The cruelest part? She loved him. She loved him long before the vows, long before the lies… so deeply it blinded her to who he really was.

Now, to make him jealous, she turns to the one man she should never touch: Jaxx Moretti, her husband’s younger brother. The dangerous one. The black sheep of the Sinclair family. The man who once made her high school years hell… and now has every reason to destroy her husband''s legacy.

What starts as a twisted game soon ignites into something raw, addictive, and completely forbidden.

But Jaxx isn’t just her escape. He’s everything her husband isn’t. Because the deeper she sinks into Jaxx''s bed… The harder it becomes to crawl back out.

Content Warning: This book contains mature themes intended for adult audiences (18+), including explicit sexual content, toxic relationships, manipulation, and emotional trauma. Reader discretion is strongly advised.',
  '{"Revenge","Explicit"}',
  102000,
  1400000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000019-ch1',
  10000019,
  0,
  'Chapter 1: The Shattered Promise',
  'It was supposed to be the happiest day of Until''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Until murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Until walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Until would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of Tangled With The Other Brother was just beginning, and every choice would shape the destiny of Nyx Rae''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000019-ch2',
  10000019,
  1,
  'Chapter 2: A New Path',
  'It was supposed to be the happiest day of Until''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Until murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Until walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Until would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of Tangled With The Other Brother was just beginning, and every choice would shape the destiny of Nyx Rae''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000019-ch3',
  10000019,
  2,
  'Chapter 3: Unexpected Alliance',
  'It was supposed to be the happiest day of Until''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Until murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Until walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Until would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of Tangled With The Other Brother was just beginning, and every choice would shape the destiny of Nyx Rae''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000019-ch4',
  10000019,
  3,
  'Chapter 4: The Unveiled Power',
  'It was supposed to be the happiest day of Until''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Until murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Until walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Until would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of Tangled With The Other Brother was just beginning, and every choice would shape the destiny of Nyx Rae''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000019-ch5',
  10000019,
  4,
  'Chapter 5: Rising from the Ashes',
  'It was supposed to be the happiest day of Until''s life. Instead, it was the day the world collapsed. Watching the two people trusted most laugh behind closed doors, planning the theft of the family inheritance and the wedding, was a pain worse than death.

"They think I am weak, that I will cry and beg for mercy," Until murmured, wiping away the tears. The fire of sorrow had burned out, leaving only the cold ash of determination. "Let them have their little wedding. They have no idea what they have truly unleashed."

An hour later, Until walked out of the estate, carrying nothing but a small key to a safety deposit box. The grandfather''s secret inheritance—the controlling shares of the entire corporate empire—was now active. The hunt had officially started.

Every step away from the house felt like a vow of vengeance. One by one, Until would tear down their perfect world, piece by piece, until they had nothing left but regret.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of Tangled With The Other Brother was just beginning, and every choice would shape the destiny of Nyx Rae''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count, coin_cost_per_thousand, start_pay_chapter_index) VALUES (
  10000020,
  'Rejected by her Alpha Chosen By the Moon ',
  'Islaadyl',
  '/assets/rejected_by_her_alpha_chosen_by_the_moon_.png',
  4.9,
  'Ongoing',
  'Myra Thorne was born of noble blood, the daughter of the Alpha King’s most trusted Betas. She was destined to be the Luna—the future queen of the pack. But when her wolf failed to appear, she became nothing but a disgrace.

On the night their fates were meant to be sealed, he shattered her world.

“I, Alpha Kaden Wolfshade, reject you as my mate.”

Humiliated and broken, she fled, hiding the greatest secrets of all the Alpha’s unborn heirs and her heritage.

Years later, when a deadly rogue uprising threatens to destroy his pack, the Alpha is forced to seek help from the one woman he cast aside.

But she’s no longer weak. No longer his. And the dark secrets she guards will change everything.',
  '{"Getting Back Together","Romance","Rejection","Werewolf","Twins"}',
  69000,
  5800000,
  500,
  3
) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count, coin_cost_per_thousand = EXCLUDED.coin_cost_per_thousand, start_pay_chapter_index = EXCLUDED.start_pay_chapter_index;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000020-ch1',
  10000020,
  0,
  'Chapter 1: The Shattered Promise',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Myra stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Myra.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Myra had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Myra''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Myra; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Myra turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was setting the stage, the discovery of betrayal or the turning point of life. The journey of Rejected by her Alpha Chosen By the Moon  was just beginning, and every choice would shape the destiny of Islaadyl''s creations.',
  800,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000020-ch2',
  10000020,
  1,
  'Chapter 2: A New Path',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Myra stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Myra.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Myra had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Myra''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Myra; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Myra turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was confronting the antagonist or making a hard decision to change the destiny. The journey of Rejected by her Alpha Chosen By the Moon  was just beginning, and every choice would shape the destiny of Islaadyl''s creations.',
  850,
  false,
  0
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000020-ch3',
  10000020,
  2,
  'Chapter 3: Unexpected Alliance',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Myra stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Myra.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Myra had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Myra''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Myra; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Myra turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was meeting a mysterious benefactor or an unexpected ally who offers help. The journey of Rejected by her Alpha Chosen By the Moon  was just beginning, and every choice would shape the destiny of Islaadyl''s creations.',
  900,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000020-ch4',
  10000020,
  3,
  'Chapter 4: The Unveiled Power',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Myra stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Myra.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Myra had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Myra''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Myra; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Myra turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was starting to reveal hidden capabilities or taking action against the enemies. (Locked Chapter) The journey of Rejected by her Alpha Chosen By the Moon  was just beginning, and every choice would shape the destiny of Islaadyl''s creations.',
  950,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (
  '10000020-ch5',
  10000020,
  4,
  'Chapter 5: Rising from the Ashes',
  'The night air of the Blue Moon forest was cold, carrying the damp scent of pine and oncoming rain. Myra stood at the border of the territory, gazing at the packhouse where warm lights shone. Inside, the sound of laughter and celebration echoed, but none of it belonged to Myra.

It was the night of the ceremony, the day when the Alpha would choose a mate. But instead of the bond of love Myra had expected, there was only betrayal. The pack''s whispers were loud and merciless, tearing down years of loyalty in a single heartbeat.

"A wolf without a strong lineage is nothing to the pack," the elder''s voice echoed in Myra''s memory. Yet, deep within, a spark of ancient power began to stir. The Moon Goddess had not abandoned Myra; she had merely set a different path, one paved with blood and retribution.

With a final, lingering look at the blue banners of the Blue Moon pack, Myra turned back and stepped into the dark expanse of the neutral lands. The forest welcomed the new exile, and a low growl of determination rumbled in the night.

This was the first victory or realization of the long path ahead. (Locked Chapter) The journey of Rejected by her Alpha Chosen By the Moon  was just beginning, and every choice would shape the destiny of Islaadyl''s creations.',
  1000,
  true,
  50
) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;

