/*
 * Copyright (C) 2022 Temporal Technologies, Inc. All Rights Reserved.
 *
 * Copyright (C) 2012-2016 Amazon.com, Inc. or its affiliates. All Rights Reserved.
 *
 * Modifications copyright (C) 2017 Uber Technologies, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this material except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package io.superdurable.dex.core;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.gen.models.EncodedObject;

public interface ObjectEncoder {
    /**
     * Each {@link ObjectEncoder} has an Encoding Type that it handles.
     *
     * @return encoding type that this converter handles.
     */
    String getEncodingType();

    /**
     * Encode a Java object to and EncodedObject
     *
     * @param object Java object to convert
     * @return encoded object with the encoding type of the encoder
     */
    EncodedObject encode(Object object);

    /**
     * Decode an encoded object into a Java object with input type
     * @param encodedObject encoded object to decode
     * @param type Java class to decode into
     * @param <T> Java class to decode into
     * @return decoded Java object
     */
    <T> T decode(EncodedObject encodedObject, Class<T> type);

    ObjectMapper getObjectMapper();
}
